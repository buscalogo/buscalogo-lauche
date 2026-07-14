package p2pdomain

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	pb "github.com/libp2p/go-libp2p-pubsub/pb"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"

	blcrypto "buscalogo-agent/internal/crypto"
	"buscalogo-agent/internal/ledger"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
)

const (
	defaultTopic    = "/buscalogo/bl/v1"
	syncProtocol    = protocol.ID("/buscalogo/bl/sync/1.0.0")
	discoverPortOff = 1 // 4401 → 4402
	discoverWorkers = 10
	discoverDialTO  = 2500 * time.Millisecond
	remoteDialTO    = 10 * time.Second // peers estáticos/known via mesh Ygg (outra rede)
)

// YggPeersFn devolve IPv6 Ygg de peers conectados na mesh.
type YggPeersFn func() []string

// SyncResult resume um ciclo de discovery + catch-up.
type SyncResult struct {
	Tried     int      `json:"tried"`
	Connected int      `json:"connected"`
	Applied   int      `json:"applied"`
	Peers     int      `json:"peers"`
	Errors    []string `json:"errors,omitempty"`
}

// Service é o GossipSub de domínios .bl sobre Yggdrasil (não confundir com P2P de busca).
type Service struct {
	buf         *logx.Buffer
	eng         *ledger.Engine
	topic       string
	port        int
	yggPeers    YggPeersFn
	staticPeers []string
	priorityIPs map[string]bool

	mu          sync.Mutex
	cancel      context.CancelFunc
	host        host.Host
	ps          *pubsub.PubSub
	t           *pubsub.Topic
	sub         *pubsub.Subscription
	discLn      net.Listener
	selfYgg     string
	syncOK      int
	serveOK     int // eventos enviados a peers (pull)
	lastSync    time.Time
	lastServe   time.Time
	discovered  int
	syncing     bool
	beaconTried map[string]time.Time
}

func New(eng *ledger.Engine, buf *logx.Buffer, topic string, port int) *Service {
	if topic == "" {
		topic = defaultTopic
	}
	if port == 0 {
		port = 4401
	}
	return &Service{eng: eng, buf: buf, topic: topic, port: port, priorityIPs: map[string]bool{}}
}

func (s *Service) SetYggPeersFn(fn YggPeersFn) { s.yggPeers = fn }

// SetStaticPeers define IPv6 Ygg de Agents conhecidos (outra rede / bootstrap manual).
func (s *Service) SetStaticPeers(ips []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(ips))
	seen := map[string]bool{}
	if s.priorityIPs == nil {
		s.priorityIPs = map[string]bool{}
	}
	for _, raw := range ips {
		ip := normalizeYggIP(raw)
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip)
		s.priorityIPs[ip] = true
	}
	s.staticPeers = out
}

// AddStaticPeer memoriza um Agent remoto.
func (s *Service) AddStaticPeer(yggIP string) (string, error) {
	ip := normalizeYggIP(yggIP)
	if ip == "" {
		return "", fmt.Errorf("IPv6 Ygg inválido")
	}
	s.mu.Lock()
	self := s.selfYgg
	s.mu.Unlock()
	if ip == self {
		return "", fmt.Errorf("não pode adicionar o próprio endereço")
	}
	rememberAgent(ip, "", false)
	s.mu.Lock()
	if s.priorityIPs == nil {
		s.priorityIPs = map[string]bool{}
	}
	s.priorityIPs[ip] = true
	found := false
	for _, p := range s.staticPeers {
		if p == ip {
			found = true
			break
		}
	}
	if !found {
		s.staticPeers = append(s.staticPeers, ip)
	}
	s.mu.Unlock()
	return ip, nil
}

// StaticPeers devolve a lista atual.
func (s *Service) StaticPeers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.staticPeers...)
}

// Start sobe libp2p + discovery TCP + GossipSub + beacon LAN.
func (s *Service) Start(ctx context.Context, yggIP string, identity ed25519.PrivateKey, bootstrap []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return nil
	}
	yggIP = strings.TrimSpace(yggIP)
	if yggIP == "" {
		return fmt.Errorf("Yggdrasil IPv6 indisponível para GossipSub")
	}
	yggIP = strings.Trim(yggIP, "[]")
	if i := strings.IndexByte(yggIP, '%'); i >= 0 {
		yggIP = yggIP[:i]
	}
	s.selfYgg = yggIP

	priv, err := identityOrPersistent(identity)
	if err != nil {
		return err
	}
	listen, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip6/%s/tcp/%d", yggIP, s.port))
	if err != nil {
		return err
	}
	h, err := libp2p.New(
		libp2p.Identity(priv),
		libp2p.ListenAddrs(listen),
		libp2p.DisableRelay(),
	)
	if err != nil {
		return fmt.Errorf("libp2p host: %w", err)
	}
	h.SetStreamHandler(syncProtocol, s.handleSyncStream)

	runCtx, cancel := context.WithCancel(ctx)
	ps, err := pubsub.NewGossipSub(runCtx, h,
		pubsub.WithMessageIdFn(func(m *pb.Message) string {
			return hex.EncodeToString(blcrypto.Hash(m.GetData()))
		}),
		pubsub.WithMaxMessageSize(1<<20),
	)
	if err != nil {
		cancel()
		_ = h.Close()
		return err
	}
	_ = ps.RegisterTopicValidator(s.topic, func(ctx context.Context, pid peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
		ev, err := ledger.UnmarshalEvent(msg.GetData())
		if err != nil {
			return pubsub.ValidationReject
		}
		if err := ledger.ValidateBasic(ev, time.Now().UnixMilli()); err != nil {
			return pubsub.ValidationReject
		}
		return pubsub.ValidationAccept
	})

	t, err := ps.Join(s.topic)
	if err != nil {
		cancel()
		_ = h.Close()
		return err
	}
	sub, err := t.Subscribe()
	if err != nil {
		cancel()
		_ = h.Close()
		return err
	}

	// Escuta em todas as ifs IPv6 — conexões chegam via Ygg TUN com dest = self Ygg.
	discLn, err := net.Listen("tcp6", fmt.Sprintf("[::]:%d", s.port+discoverPortOff))
	if err != nil {
		discLn, err = net.Listen("tcp", net.JoinHostPort(yggIP, fmt.Sprintf("%d", s.port+discoverPortOff)))
	}
	if err != nil {
		cancel()
		_ = h.Close()
		return fmt.Errorf("discovery listen: %w", err)
	}

	s.cancel = cancel
	s.host = h
	s.ps = ps
	s.t = t
	s.sub = sub
	s.discLn = discLn
	s.eng.SetPublisher(s)

	go s.loop(runCtx)
	go s.serveDiscovery(runCtx)
	go s.connectBootstrap(runCtx, bootstrap)
	go s.discoveryLoop(runCtx)
	go s.beaconLoop(runCtx)
	go s.listenBeacon(runCtx)

	if s.buf != nil {
		s.buf.Infof("p2pdomain", "GossipSub %s + discover :%d + beacon :%d peer=%s",
			listen, s.port+discoverPortOff, s.port+beaconPortOff, h.ID())
	}
	return nil
}

func (s *Service) loop(ctx context.Context) {
	for {
		msg, err := s.sub.Next(ctx)
		if err != nil {
			return
		}
		if s.host != nil && msg.ReceivedFrom == s.host.ID() {
			continue
		}
		if err := s.eng.Ingest(msg.GetData()); err != nil && s.buf != nil {
			s.buf.Warnf("p2pdomain", "ingest: %v", err)
		}
	}
}

type helloMsg struct {
	V      int      `json:"v"`
	PeerID string   `json:"peer_id"`
	Addrs  []string `json:"addrs"`
	YggIP  string   `json:"ygg_ip"`
}

func (s *Service) serveDiscovery(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = s.discLn.Close()
	}()
	for {
		conn, err := s.discLn.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		go s.handleDiscoverConn(conn)
	}
}

func (s *Service) handleDiscoverConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	s.mu.Lock()
	h := s.host
	ygg := s.selfYgg
	s.mu.Unlock()
	if h == nil {
		return
	}
	var addrs []string
	for _, a := range h.Addrs() {
		addrs = append(addrs, a.String()+"/p2p/"+h.ID().String())
	}
	_ = json.NewEncoder(conn).Encode(helloMsg{
		V: 1, PeerID: h.ID().String(), Addrs: addrs, YggIP: ygg,
	})
}

func (s *Service) discoveryLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	s.SyncNow(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.SyncNow(ctx)
		}
	}
}

func (s *Service) candidateIPs() []string {
	s.mu.Lock()
	self := s.selfYgg
	static := append([]string(nil), s.staticPeers...)
	s.mu.Unlock()
	var ygg []string
	if s.yggPeers != nil {
		ygg = s.yggPeers()
	}
	known := knownAgentIPs(self)
	s.mu.Lock()
	if s.priorityIPs == nil {
		s.priorityIPs = map[string]bool{}
	}
	for _, ip := range known {
		s.priorityIPs[ip] = true
	}
	for _, ip := range static {
		s.priorityIPs[ip] = true
	}
	s.mu.Unlock()
	// Static + known primeiro (outra rede); seeds Ygg por último.
	return mergeUniqueIPs(self, static, known, ygg)
}

// SyncNow faz discovery paralelo + catch-up e espera terminar.
func (s *Service) SyncNow(ctx context.Context) SyncResult {
	s.mu.Lock()
	if s.host == nil {
		s.mu.Unlock()
		return SyncResult{Errors: []string{"gossip não iniciado"}}
	}
	if s.syncing {
		peers := len(s.host.Network().Peers())
		s.mu.Unlock()
		return SyncResult{Peers: peers, Errors: []string{"sync já em andamento"}}
	}
	s.syncing = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.syncing = false
		s.mu.Unlock()
	}()

	ips := s.candidateIPs()
	res := SyncResult{Tried: len(ips)}
	if len(ips) == 0 {
		s.mu.Lock()
		if s.host != nil {
			res.Peers = len(s.host.Network().Peers())
		}
		s.mu.Unlock()
		if s.buf != nil {
			s.buf.Warnf("p2pdomain", "sync: nenhum peer — em outra rede adicione o IPv6 Ygg do outro Agent")
		}
		res.Errors = append(res.Errors, "sem candidatos; adicione IPv6 Ygg do outro Agent")
		return res
	}

	type hit struct {
		ip   string
		pid  peer.ID
		pull int
		push int
		err  error
	}
	jobs := make(chan string, len(ips))
	out := make(chan hit, len(ips))
	workers := discoverWorkers
	if workers > len(ips) {
		workers = len(ips)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				pid, err := s.discoverAndConnect(ctx, ip)
				if err != nil {
					out <- hit{ip: ip, err: err}
					continue
				}
				pulled, err := s.pullSyncCounted(ctx, pid)
				if err != nil {
					out <- hit{ip: ip, pid: pid, pull: pulled, err: err}
					continue
				}
				pushed, _ := s.pushSyncCounted(ctx, pid)
				out <- hit{ip: ip, pid: pid, pull: pulled, push: pushed}
			}
		}()
	}
	for _, ip := range ips {
		jobs <- ip
	}
	close(jobs)
	go func() {
		wg.Wait()
		close(out)
	}()

	pushedTotal := 0
	for h := range out {
		if h.err != nil {
			if !isBenignDialErr(h.err) {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", h.ip, h.err))
				if s.buf != nil {
					s.buf.Warnf("p2pdomain", "discover %s: %v", h.ip, h.err)
				}
			}
			continue
		}
		res.Connected++
		res.Applied += h.pull
		pushedTotal += h.push
		rememberAgent(h.ip, h.pid.String(), true)
		s.mu.Lock()
		s.discovered++
		s.mu.Unlock()
		if s.buf != nil {
			s.buf.Infof("p2pdomain", "peer %s (%s): pull +%d / push %d eventos", h.pid, h.ip, h.pull, h.push)
		}
	}
	_ = pushedTotal


	s.mu.Lock()
	if s.host != nil {
		res.Peers = len(s.host.Network().Peers())
	}
	if res.Applied > 0 || res.Connected > 0 {
		s.lastSync = time.Now()
	}
	s.mu.Unlock()
	if s.buf != nil && (res.Connected > 0 || res.Applied > 0) {
		s.buf.Infof("p2pdomain", "sync ciclo: tried=%d connected=%d applied=%d gossip_peers=%d",
			res.Tried, res.Connected, res.Applied, res.Peers)
	}
	return res
}

func (s *Service) discoverAndConnect(ctx context.Context, yggIP string) (peer.ID, error) {
	s.mu.Lock()
	h := s.host
	port := s.port
	slow := s.priorityIPs != nil && s.priorityIPs[yggIP]
	s.mu.Unlock()
	if h == nil {
		return "", fmt.Errorf("host nil")
	}
	addr := net.JoinHostPort(yggIP, fmt.Sprintf("%d", port+discoverPortOff))
	to := discoverDialTO
	if slow {
		to = remoteDialTO
	}
	d := net.Dialer{Timeout: to}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	var hello helloMsg
	if err := json.NewDecoder(conn).Decode(&hello); err != nil {
		return "", err
	}
	if hello.PeerID == "" {
		return "", fmt.Errorf("hello sem peer_id")
	}
	pid, err := peer.Decode(hello.PeerID)
	if err != nil {
		return "", err
	}
	if pid == h.ID() {
		return pid, nil
	}
	var mas []multiaddr.Multiaddr
	for _, a := range hello.Addrs {
		ma, err := multiaddr.NewMultiaddr(a)
		if err == nil {
			mas = append(mas, ma)
		}
	}
	if len(mas) == 0 {
		ma, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip6/%s/tcp/%d/p2p/%s", yggIP, port, hello.PeerID))
		if err != nil {
			return "", err
		}
		mas = append(mas, ma)
	}
	info := peer.AddrInfo{ID: pid, Addrs: mas}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := h.Connect(cctx, info); err != nil {
		if h.Network().Connectedness(pid) != network.Connected {
			return "", err
		}
	}
	return pid, nil
}

func (s *Service) pullSyncCounted(ctx context.Context, pid peer.ID) (int, error) {
	s.mu.Lock()
	h := s.host
	s.mu.Unlock()
	if h == nil {
		return 0, fmt.Errorf("host nil")
	}
	if pid == h.ID() {
		return 0, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stream, err := h.NewStream(cctx, pid, syncProtocol)
	if err != nil {
		return 0, err
	}
	defer stream.Close()
	_ = json.NewEncoder(stream).Encode(map[string]string{"cmd": "pull"})
	var resp struct {
		Cmd    string            `json:"cmd"`
		Count  int               `json:"count"`
		Events []json.RawMessage `json:"events"`
	}
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return 0, err
	}
	raws := make([][]byte, 0, len(resp.Events))
	for _, raw := range resp.Events {
		raws = append(raws, []byte(raw))
	}
	applied, skipped, failed, errs := s.eng.IngestHistoricalBatch(raws)
	s.mu.Lock()
	s.syncOK += applied
	if applied > 0 {
		s.lastSync = time.Now()
	}
	s.mu.Unlock()
	if s.buf != nil {
		if applied > 0 || failed > 0 {
			s.buf.Infof("p2pdomain", "pull %s: novos=%d skip=%d falha=%d (recv=%d)",
				pid, applied, skipped, failed, len(raws))
		}
		for _, e := range errs {
			s.buf.Warnf("p2pdomain", "pull ingest: %s", e)
		}
	}
	return applied, nil
}

// pushSyncCounted envia o ledger local ao peer (registry/seed precisa receber registros).
func (s *Service) pushSyncCounted(ctx context.Context, pid peer.ID) (int, error) {
	s.mu.Lock()
	h := s.host
	s.mu.Unlock()
	if h == nil {
		return 0, fmt.Errorf("host nil")
	}
	if pid == h.ID() {
		return 0, nil
	}
	evs, err := s.eng.ExportAllEvents()
	if err != nil || len(evs) == 0 {
		return 0, err
	}
	out := make([]json.RawMessage, 0, len(evs))
	for _, e := range evs {
		out = append(out, json.RawMessage(e))
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stream, err := h.NewStream(cctx, pid, syncProtocol)
	if err != nil {
		return 0, err
	}
	defer stream.Close()
	if err := json.NewEncoder(stream).Encode(map[string]any{
		"cmd":    "push",
		"count":  len(out),
		"events": out,
	}); err != nil {
		return 0, err
	}
	var resp struct {
		Cmd     string `json:"cmd"`
		Applied int    `json:"applied"`
	}
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		// Peers antigos não entendem push — ok.
		return 0, nil
	}
	if s.buf != nil && resp.Applied > 0 {
		s.buf.Infof("p2pdomain", "push sync → %s: %d aplicados no peer", pid, resp.Applied)
	}
	return resp.Applied, nil
}

func (s *Service) handleSyncStream(stream network.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(30 * time.Second))
	remote := stream.Conn().RemotePeer()
	var req struct {
		Cmd    string            `json:"cmd"`
		Count  int               `json:"count"`
		Events []json.RawMessage `json:"events"`
	}
	if err := json.NewDecoder(stream).Decode(&req); err != nil {
		return
	}
	switch req.Cmd {
	case "pull":
		evs, err := s.eng.ExportAllEvents()
		if err != nil {
			if s.buf != nil {
				s.buf.Warnf("p2pdomain", "serve sync %s: export: %v", remote, err)
			}
			return
		}
		out := make([]json.RawMessage, 0, len(evs))
		for _, e := range evs {
			out = append(out, json.RawMessage(e))
		}
		if err := json.NewEncoder(stream).Encode(map[string]any{
			"cmd":    "events",
			"count":  len(out),
			"events": out,
		}); err != nil {
			if s.buf != nil {
				s.buf.Warnf("p2pdomain", "serve sync %s: write: %v", remote, err)
			}
			return
		}
		s.mu.Lock()
		s.serveOK += len(out)
		s.lastServe = time.Now()
		s.mu.Unlock()
		if s.buf != nil {
			s.buf.Infof("p2pdomain", "serve sync → %s: %d eventos", remote, len(out))
		}
	case "push":
		raws := make([][]byte, 0, len(req.Events))
		for _, raw := range req.Events {
			raws = append(raws, []byte(raw))
		}
		applied, skipped, failed, errs := s.eng.IngestHistoricalBatch(raws)
		_ = json.NewEncoder(stream).Encode(map[string]any{
			"cmd":     "push_ok",
			"applied": applied,
			"skipped": skipped,
			"failed":  failed,
		})
		if s.buf != nil {
			s.buf.Infof("p2pdomain", "recv push ← %s: novos=%d skip=%d falha=%d", remote, applied, skipped, failed)
			for _, e := range errs {
				s.buf.Warnf("p2pdomain", "push ingest: %s", e)
			}
		}
	}
}

func (s *Service) connectBootstrap(ctx context.Context, peers []string) {
	for _, raw := range peers {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ma, err := multiaddr.NewMultiaddr(raw)
		if err != nil {
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = s.host.Connect(cctx, *info)
		cancel()
		if err == nil {
			_, _ = s.pullSyncCounted(ctx, info.ID)
		}
	}
}

// ForceSync dispara sync em background (compat).
func (s *Service) ForceSync() {
	go s.SyncNow(context.Background())
}

// Publish implementa ledger.Publisher.
func (s *Service) Publish(raw []byte) error {
	s.mu.Lock()
	t := s.t
	s.mu.Unlock()
	if t == nil {
		return fmt.Errorf("gossip não iniciado")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return t.Publish(ctx, raw)
}

func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.discLn != nil {
		_ = s.discLn.Close()
		s.discLn = nil
	}
	if s.sub != nil {
		s.sub.Cancel()
		s.sub = nil
	}
	if s.t != nil {
		_ = s.t.Close()
		s.t = nil
	}
	if s.host != nil {
		err := s.host.Close()
		s.host = nil
		return err
	}
	return nil
}

func (s *Service) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{
		"running":     s.host != nil,
		"topic":       s.topic,
		"port":        s.port,
		"discover":    s.port + discoverPortOff,
		"beacon":      s.port + beaconPortOff,
		"sync_events":  s.syncOK,
		"serve_events": s.serveOK,
		"discovered":   s.discovered,
		"syncing":      s.syncing,
	}
	if !s.lastSync.IsZero() {
		out["last_sync"] = s.lastSync.Format(time.RFC3339)
	}
	if !s.lastServe.IsZero() {
		out["last_serve"] = s.lastServe.Format(time.RFC3339)
	}
	if s.host != nil {
		out["peer_id"] = s.host.ID().String()
		var addrs []string
		for _, a := range s.host.Addrs() {
			addrs = append(addrs, a.String()+"/p2p/"+s.host.ID().String())
		}
		out["addrs"] = addrs
		out["peers"] = len(s.host.Network().Peers())
	}
	known := knownAgentIPs(s.selfYgg)
	out["known_agents"] = len(known)
	out["static_peers"] = append([]string(nil), s.staticPeers...)
	return out
}

func normalizeYggIP(ip string) string {
	ip = strings.TrimSpace(ip)
	ip = strings.Trim(ip, "[]")
	if i := strings.IndexByte(ip, '/'); i >= 0 {
		ip = ip[:i]
	}
	if i := strings.IndexByte(ip, '%'); i >= 0 {
		ip = ip[:i]
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	return parsed.String()
}

func identityOrPersistent(prefer ed25519.PrivateKey) (crypto.PrivKey, error) {
	// Preferência: chave de conta (64 bytes). libp2p UnmarshalEd25519PrivateKey
	// espera 64 (seed||pub) ou 96 — NÃO o seed isolado de 32 bytes.
	if len(prefer) == ed25519.PrivateKeySize {
		return crypto.UnmarshalEd25519PrivateKey([]byte(prefer))
	}
	path, err := domainKeyPath()
	if err != nil {
		p, _, e := crypto.GenerateEd25519Key(rand.Reader)
		return p, e
	}
	if raw, err := os.ReadFile(path); err == nil {
		decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err == nil {
			switch len(decoded) {
			case ed25519.PrivateKeySize:
				return crypto.UnmarshalEd25519PrivateKey(decoded)
			case ed25519.SeedSize:
				full := ed25519.NewKeyFromSeed(decoded)
				return crypto.UnmarshalEd25519PrivateKey([]byte(full))
			}
		}
	}
	p, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	raw, err := p.Raw()
	if err == nil {
		// Persiste a chave completa (64 bytes hex) para reloads futuros.
		_ = os.WriteFile(path, []byte(hex.EncodeToString(raw)), 0o600)
	}
	return p, nil
}

func domainKeyPath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "identity")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "p2pdomain.key"), nil
}
