package p2p

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/scraper"

	"github.com/gorilla/websocket"
)

const (
	maxReconnectAttempts = 5
	reconnectBaseDelay   = 5 * time.Second
	connectTimeout       = 10 * time.Second
	heartbeatInterval    = 30 * time.Second
	readWaitTimeout      = 120 * time.Second
)

type Connector struct {
	cfg    *config.Config
	store  *scraper.Store
	buf    *logx.Buffer
	peerID string

	mu          sync.RWMutex
	connections map[string]*connState
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	running     bool
	startError  string

	stats Stats
}

type connState struct {
	url               string
	ws                *websocket.Conn
	writeMu           sync.Mutex
	reconnectAttempts int
	lastError         string
	state             string // connecting, connected, reconnecting, error, gave_up, stopped
}

type SignalingStatus struct {
	URL       string `json:"url"`
	State     string `json:"state"`
	LastError string `json:"last_error,omitempty"`
	Attempts  int    `json:"attempts"`
}

type Stats struct {
	PeerID           string            `json:"peer_id"`
	QueriesReceived  int64             `json:"queries_received"`
	QueriesResponded int64             `json:"queries_responded"`
	TotalResultsSent int64             `json:"total_results_sent"`
	Connected        bool              `json:"connected"`
	ConnectedCount   int               `json:"connected_count"`
	TotalSignalings  int               `json:"total_signalings"`
	ConnectedAt      int64             `json:"connected_at,omitempty"`
	LastQueryAt      int64             `json:"last_query_at,omitempty"`
	UptimeMs         int64             `json:"uptime_ms"`
	Enabled          bool              `json:"enabled"`
	Running          bool              `json:"running"`
	Message          string            `json:"message,omitempty"`
	StartError       string            `json:"start_error,omitempty"`
	Signalings       []SignalingStatus `json:"signalings,omitempty"`
}

type inboundMsg struct {
	Type      string `json:"type"`
	QueryID   string `json:"queryId"`
	Query     string `json:"query"`
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}

func New(cfg *config.Config, store *scraper.Store, buf *logx.Buffer) *Connector {
	return &Connector{
		cfg:         cfg,
		store:       store,
		buf:         buf,
		peerID:      newPeerID(),
		connections: make(map[string]*connState),
	}
}

func newPeerID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("scraper_server_%d_%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

func (c *Connector) Start() error {
	if c.store == nil {
		err := "CouchDB/scraper indisponível — o P2P precisa do banco buscalogo_scraping"
		c.mu.Lock()
		c.startError = err
		c.running = false
		c.mu.Unlock()
		return fmt.Errorf("p2p: %s", err)
	}
	urls := c.cfg.P2P.SignalingURLs
	if len(urls) == 0 {
		err := "nenhuma URL de signaling configurada"
		c.mu.Lock()
		c.startError = err
		c.running = false
		c.mu.Unlock()
		return fmt.Errorf("p2p: %s", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.cancel = cancel
	c.running = true
	c.startError = ""
	c.connections = make(map[string]*connState)
	for _, u := range urls {
		c.connections[u] = &connState{url: u, state: "connecting"}
	}
	c.mu.Unlock()

	c.buf.Infof("p2p", "conectando a %d signaling(s) peerId=%s", len(urls), c.peerID)

	ok := 0
	for _, u := range urls {
		if err := c.connect(ctx, u); err != nil {
			c.setConnError(u, err)
			c.buf.Warnf("p2p", "falha ao conectar %s: %v", u, err)
			c.wg.Add(1)
			go c.reconnectLoop(ctx, u)
		} else {
			ok++
		}
	}
	if ok == 0 {
		c.buf.Warnf("p2p", "nenhum signaling conectado ainda; reconexão em background")
	} else {
		c.buf.Infof("p2p", "%d/%d signaling(s) conectado(s)", ok, len(urls))
	}
	return nil
}

func (c *Connector) Stop() error {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.running = false
	conns := c.connections
	for _, st := range conns {
		st.state = "stopped"
		st.ws = nil
	}
	c.connections = make(map[string]*connState)
	c.stats.ConnectedAt = 0
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, st := range conns {
		if st.ws != nil {
			_ = st.ws.Close()
		}
	}
	c.wg.Wait()
	c.buf.Infof("p2p", "conector parado")
	return nil
}

func (c *Connector) Restart() error {
	if err := c.Stop(); err != nil {
		return err
	}
	if !c.cfg.P2PEnabled() {
		c.buf.Infof("p2p", "reinício ignorado — P2P desabilitado na config")
		return nil
	}
	return c.Start()
}

func (c *Connector) GetStats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	connected := 0
	for _, st := range c.connections {
		if st.ws != nil {
			connected++
		}
	}
	s := c.stats
	s.PeerID = c.peerID
	s.Connected = connected > 0
	s.ConnectedCount = connected
	s.Enabled = c.cfg.P2PEnabled()
	s.Running = c.running
	s.StartError = c.startError
	s.TotalSignalings = len(c.cfg.P2P.SignalingURLs)
	if s.ConnectedAt > 0 {
		s.UptimeMs = time.Now().UnixMilli() - s.ConnectedAt
	}
	s.Signalings = c.buildSignalingStatusesLocked()
	s.Message = c.summaryMessageLocked(connected)
	return s
}

func (c *Connector) buildSignalingStatusesLocked() []SignalingStatus {
	urls := c.cfg.P2P.SignalingURLs
	out := make([]SignalingStatus, 0, len(urls))
	seen := make(map[string]bool, len(urls))
	for _, u := range urls {
		seen[u] = true
		st := c.connections[u]
		if st == nil {
			state := "stopped"
			if c.running {
				state = "idle"
			}
			out = append(out, SignalingStatus{URL: u, State: state})
			continue
		}
		out = append(out, SignalingStatus{
			URL:       u,
			State:     st.state,
			LastError: st.lastError,
			Attempts:  st.reconnectAttempts,
		})
	}
	for u, st := range c.connections {
		if seen[u] {
			continue
		}
		out = append(out, SignalingStatus{
			URL:       u,
			State:     st.state,
			LastError: st.lastError,
			Attempts:  st.reconnectAttempts,
		})
	}
	return out
}

func (c *Connector) summaryMessageLocked(connected int) string {
	if !c.cfg.P2PEnabled() {
		return "P2P desabilitado na configuração — marque a opção, salve e clique em Reconectar"
	}
	if c.startError != "" {
		return c.startError
	}
	if !c.running {
		return "P2P parado — salve a configuração ou clique em Reconectar"
	}
	if connected > 0 {
		return fmt.Sprintf("%d/%d signaling(s) conectado(s)", connected, len(c.cfg.P2P.SignalingURLs))
	}
	reconnecting := 0
	for _, sig := range c.buildSignalingStatusesLocked() {
		if sig.State == "reconnecting" || sig.State == "connecting" {
			reconnecting++
		}
	}
	if reconnecting > 0 {
		return fmt.Sprintf("reconectando a %d signaling(s)...", reconnecting)
	}
	var errs []string
	for _, sig := range c.buildSignalingStatusesLocked() {
		if sig.LastError != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", sig.URL, sig.LastError))
		} else if sig.State == "gave_up" {
			errs = append(errs, fmt.Sprintf("%s: desistiu após %d tentativas", sig.URL, sig.Attempts))
		}
	}
	if len(errs) > 0 {
		return strings.Join(errs, " · ")
	}
	return "aguardando conexão com signaling..."
}

func (c *Connector) setConnError(serverURL string, err error) {
	msg := friendlyDialError(err)
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.connections[serverURL]
	if !ok {
		st = &connState{url: serverURL}
		c.connections[serverURL] = st
	}
	st.ws = nil
	st.state = "error"
	st.lastError = msg
	st.reconnectAttempts++
}

func (c *Connector) setConnConnected(serverURL string, ws *websocket.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.connections[serverURL]
	if !ok {
		st = &connState{url: serverURL}
		c.connections[serverURL] = st
	}
	st.ws = ws
	st.state = "connected"
	st.lastError = ""
	st.reconnectAttempts = 0
	if c.stats.ConnectedAt == 0 {
		c.stats.ConnectedAt = time.Now().UnixMilli()
	}
}

func (c *Connector) TestSearch(query string) ([]scraper.SearchHit, error) {
	if c.store == nil {
		return nil, fmt.Errorf("store indisponível")
	}
	limit := c.cfg.P2P.MaxResultsPerQuery
	if limit <= 0 {
		limit = 50
	}
	return c.store.Search(query, "test_query", c.peerID, limit)
}

func (c *Connector) connect(ctx context.Context, serverURL string) error {
	u, err := url.Parse(serverURL)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("peerId", c.peerID)
	u.RawQuery = q.Encode()

	c.mu.Lock()
	if st, ok := c.connections[serverURL]; ok {
		st.state = "connecting"
		st.lastError = ""
	}
	c.mu.Unlock()

	dialer := websocket.Dialer{HandshakeTimeout: connectTimeout}
	header := http.Header{}
	ws, _, err := dialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return err
	}

	c.setConnConnected(serverURL, ws)
	c.send(serverURL, ws, map[string]any{
		"type":     "PEER_CONNECT",
		"peerId":   c.peerID,
		"peerType": "scraper_server",
	})
	c.buf.Infof("p2p", "conectado a %s", serverURL)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.heartbeatLoop(ctx, serverURL, ws)
	}()
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.readLoop(ctx, serverURL, ws)
	}()
	return nil
}

func (c *Connector) heartbeatLoop(ctx context.Context, serverURL string, ws *websocket.Conn) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	// primeiro PING logo após conectar (proxies/nginx costumam fechar conexões ociosas)
	c.send(serverURL, ws, map[string]any{
		"type":      "PING",
		"peerId":    c.peerID,
		"timestamp": time.Now().UnixMilli(),
	})
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			st := c.connections[serverURL]
			active := st != nil && st.ws == ws
			c.mu.RUnlock()
			if !active {
				return
			}
			c.send(serverURL, ws, map[string]any{
				"type":      "PING",
				"peerId":    c.peerID,
				"timestamp": time.Now().UnixMilli(),
			})
		}
	}
}

func (c *Connector) readLoop(ctx context.Context, serverURL string, ws *websocket.Conn) {
	defer func() {
		_ = ws.Close()
		c.scheduleReconnect(ctx, serverURL)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = ws.SetReadDeadline(time.Now().Add(readWaitTimeout))
		_, data, err := ws.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				c.buf.Warnf("p2p", "leitura %s: %v", serverURL, err)
				c.mu.Lock()
				if st, ok := c.connections[serverURL]; ok && st.ws == ws {
					st.lastError = friendlyDialError(err)
				}
				c.mu.Unlock()
			}
			return
		}
		c.handleMessage(serverURL, ws, data)
	}
}

func (c *Connector) handleMessage(serverURL string, ws *websocket.Conn, data []byte) {
	var msg inboundMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		c.buf.Warnf("p2p", "json inválido: %v", err)
		return
	}

	switch msg.Type {
	case "WELCOME", "CONNECTION_ESTABLISHED", "PONG":
		c.buf.Infof("p2p", "%s", msg.Type)
	case "PING":
		c.send(serverURL, ws, map[string]any{
			"type":      "PONG",
			"peerId":    c.peerID,
			"timestamp": time.Now().UnixMilli(),
		})
	case "SEARCH_REQUEST":
		c.handleSearch(serverURL, ws, msg.QueryID, msg.Query)
	default:
		c.buf.Infof("p2p", "mensagem: %s", msg.Type)
	}
}

func (c *Connector) handleSearch(serverURL string, ws *websocket.Conn, queryID, query string) {
	if queryID == "" || query == "" {
		return
	}
	c.buf.Infof("p2p", "SEARCH_REQUEST queryId=%s q=%q", queryID, query)

	c.mu.Lock()
	c.stats.QueriesReceived++
	c.mu.Unlock()

	limit := c.cfg.P2P.MaxResultsPerQuery
	if limit <= 0 {
		limit = 50
	}

	results, err := c.store.Search(query, queryID, c.peerID, limit)
	if err != nil {
		c.send(serverURL, ws, map[string]any{
			"type":      "SEARCH_RESPONSE",
			"queryId":   queryID,
			"error":     err.Error(),
			"peerId":    c.peerID,
			"timestamp": time.Now().UnixMilli(),
		})
		return
	}

	c.send(serverURL, ws, map[string]any{
		"type":      "SEARCH_RESPONSE",
		"queryId":   queryID,
		"results":   results,
		"peerId":    c.peerID,
		"timestamp": time.Now().UnixMilli(),
	})

	c.mu.Lock()
	c.stats.QueriesResponded++
	c.stats.TotalResultsSent += int64(len(results))
	c.stats.LastQueryAt = time.Now().UnixMilli()
	c.mu.Unlock()

	c.buf.Infof("p2p", "SEARCH_RESPONSE queryId=%s results=%d", queryID, len(results))
}

func (c *Connector) send(serverURL string, ws *websocket.Conn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.mu.RLock()
	st := c.connections[serverURL]
	c.mu.RUnlock()
	if st == nil || st.ws != ws {
		return
	}
	st.writeMu.Lock()
	defer st.writeMu.Unlock()
	if st.ws != ws {
		return
	}
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_ = ws.WriteMessage(websocket.TextMessage, data)
}

func (c *Connector) scheduleReconnect(ctx context.Context, serverURL string) {
	c.mu.Lock()
	st, ok := c.connections[serverURL]
	if !ok {
		c.mu.Unlock()
		return
	}
	st.ws = nil
	attempts := st.reconnectAttempts + 1
	st.reconnectAttempts = attempts
	st.state = "reconnecting"
	c.mu.Unlock()

	if attempts > maxReconnectAttempts {
		c.buf.Errorf("p2p", "máximo de reconexões atingido para %s", serverURL)
		c.mu.Lock()
		if st, ok := c.connections[serverURL]; ok {
			st.state = "gave_up"
			st.lastError = fmt.Sprintf("desistiu após %d tentativas de reconexão", maxReconnectAttempts)
		}
		c.mu.Unlock()
		return
	}

	delay := reconnectBaseDelay * time.Duration(1<<(attempts-1))
	c.buf.Infof("p2p", "reconectando %s (%d/%d) em %s", serverURL, attempts, maxReconnectAttempts, delay)

	time.AfterFunc(delay, func() {
		if ctx.Err() != nil {
			return
		}
		if err := c.connect(ctx, serverURL); err != nil {
			c.setConnError(serverURL, err)
			c.buf.Warnf("p2p", "reconexão %s: %v", serverURL, err)
			c.scheduleReconnect(ctx, serverURL)
		}
	})
}

func (c *Connector) reconnectLoop(ctx context.Context, serverURL string) {
	defer c.wg.Done()
	c.mu.Lock()
	if _, ok := c.connections[serverURL]; !ok {
		c.connections[serverURL] = &connState{url: serverURL, state: "connecting"}
	}
	c.mu.Unlock()
	for ctx.Err() == nil {
		c.mu.RLock()
		st := c.connections[serverURL]
		connected := st != nil && st.ws != nil
		c.mu.RUnlock()
		if connected {
			return
		}
		if err := c.connect(ctx, serverURL); err != nil {
			c.setConnError(serverURL, err)
			c.buf.Warnf("p2p", "reconexão inicial %s: %v", serverURL, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectBaseDelay):
			}
			continue
		}
		return
	}
}

func friendlyDialError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "connection refused"):
		return "conexão recusada — o servidor signaling não está rodando nesse endereço"
	case strings.Contains(low, "no such host"), strings.Contains(low, "name or service not known"):
		return "host não encontrado (DNS)"
	case strings.Contains(low, "i/o timeout"), strings.Contains(low, "timeout"):
		return "tempo esgotado ao conectar"
	case strings.Contains(low, "tls"), strings.Contains(low, "certificate"):
		return "falha TLS/SSL: " + s
	case strings.Contains(low, "close 1006"), strings.Contains(low, "unexpected eof"), strings.Contains(low, "abnormal closure"):
		return "conexão fechada pelo servidor (timeout ou rede) — reconectando automaticamente"
	default:
		return s
	}
}
