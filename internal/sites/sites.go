package sites

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
	"gopkg.in/yaml.v3"
)

// Site é uma definição de host virtual (.bl) local, combinando config em memória
// e arquivos em sites/*.yaml (estilo Apache/Nginx sites-available).
type Site struct {
	Host     string `yaml:"host" json:"host"`
	Type     string `yaml:"type" json:"type"` // static | proxy
	Root     string `yaml:"root" json:"root"` // caminho para arquivos estáticos
	Upstream string `yaml:"upstream" json:"upstream"` // URL para proxy reverso
	Enabled  bool   `yaml:"enabled" json:"enabled"`
}

type Manager struct {
	cfg          *config.Config
	buf          *logx.Buffer
	srv          *http.Server
	srvTLS       *http.Server
	mu           sync.RWMutex
	sites        []Site
	proxies      map[string]*httputil.ReverseProxy
	actualPort   int
	portFallback bool
	webRunning   bool
	webError     string
	tlsPort      int
	tlsRunning   bool
	tlsError     string
	tlsMode      string
}

func New(cfg *config.Config, buf *logx.Buffer) *Manager {
	return &Manager{cfg: cfg, buf: buf, proxies: make(map[string]*httputil.ReverseProxy), actualPort: cfg.Web.Port}
}

// LoadSites combina sites da config (legado/API) com arquivos sites/*.yaml.
func (m *Manager) LoadSites() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadSitesLocked()
}

// loadSitesLocked realiza o carregamento assumindo que m.mu já está lockado.
func (m *Manager) loadSitesLocked() error {
	var loaded []Site

	// 1. Sites da config
	for _, s := range m.cfg.Sites {
		loaded = append(loaded, Site{
			Host:     s.Host,
			Type:     s.Type,
			Root:     s.Root,
			Upstream: s.Upstream,
			Enabled:  s.Enabled,
		})
	}

	// 2. Arquivos em sites/ (user + system)
	for _, dir := range m.sitesDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				m.buf.Warnf("sites", "ler %s: %v", e.Name(), err)
				continue
			}
			var s Site
			if err := yaml.Unmarshal(data, &s); err != nil {
				m.buf.Warnf("sites", "parse %s: %v", e.Name(), err)
				continue
			}
			// Resolve caminhos relativos a partir do diretório raiz da instalação
			// (diretório pai de sites/), não do próprio diretório sites/.
			if s.Root != "" && !filepath.IsAbs(s.Root) {
				s.Root = filepath.Join(filepath.Dir(dir), s.Root)
			}
			loaded = append(loaded, s)
		}
	}

	// deduplica por host (último ganha)
	byHost := make(map[string]Site)
	for _, s := range loaded {
		if s.Host == "" {
			continue
		}
		if s.Type == "" {
			s.Type = "static"
		}
		byHost[strings.ToLower(s.Host)] = s
	}

	m.sites = make([]Site, 0, len(byHost))
	for _, s := range byHost {
		m.sites = append(m.sites, s)
	}

	// reinicia proxies
	m.proxies = make(map[string]*httputil.ReverseProxy)
	return nil
}

func (m *Manager) sitesDirs() []string {
	var dirs []string
	// diretório do usuário (home)
	if home, err := paths.Home(); err == nil {
		dirs = append(dirs, filepath.Join(home, "sites"))
	}
	// diretório do sistema (onde o binário está instalado)
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dirs = append(dirs, filepath.Join(filepath.Dir(exe), "sites"))
		}
	}
	return dirs
}

// ResolveRoot converte caminhos relativos (www/...) para absolutos baseados em home.
func (m *Manager) ResolveRoot(root string) string {
	if root == "" {
		return ""
	}
	if filepath.IsAbs(root) {
		return root
	}
	home, err := paths.Home()
	if err != nil {
		return root
	}
	return filepath.Join(home, root)
}

// Handler roteia por Host: static files ou proxy reverso.
// Prefixo reservado /__buscalogo_agent__/ espelha a API local (9970) para a
// extensão contornar bloqueios de loopback do Firefox (same-origin em *.bl).
func (m *Manager) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, agentAPIPrefix) {
			m.serveAgentAPIProxy(w, r)
			return
		}

		host := hostOnly(r.Host)
		site := m.findSite(host)
		if site == nil || !site.Enabled {
			// Fallback: acesso direto por IP (localhost, Yggdrasil IPv6, LAN).
			// Serve o primeiro site habilitado para que o agente sempre
			// responda algo, mesmo antes do DNS resolver .bl.
			if isIPAddress(host) || isLocalHost(host) {
				site = m.findDefaultSite()
			}
		}
		if site == nil || !site.Enabled {
			http.Error(w, "site "+host+" não hospedado neste agente", http.StatusNotFound)
			return
		}

		switch site.Type {
		case "proxy":
			m.serveProxy(w, r, site)
		case "static":
			fallthrough
		default:
			m.serveStatic(w, r, site)
		}
	})
}

const agentAPIPrefix = "/__buscalogo_agent__/"

func (m *Manager) serveAgentAPIProxy(w http.ResponseWriter, r *http.Request) {
	// CORS para content scripts / popup da extensão.
	origin := r.Header.Get("Origin")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Vary", "Origin")
	} else {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	apiListen := m.cfg.API.Listen
	if apiListen == "" {
		apiListen = "127.0.0.1:9970"
	}
	target, err := url.Parse("http://" + apiListen + "/")
	if err != nil {
		http.Error(w, "API listen inválido", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/__buscalogo_agent__")
		if path == "" {
			path = "/"
		}
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = path
		req.Host = target.Host
		// Remove hop-by-hop headers that confuse o reverse proxy local.
		req.Header.Del("Accept-Encoding")
	}
	proxy.ServeHTTP(w, r)
}

func (m *Manager) findSite(host string) *Site {
	m.mu.RLock()
	defer m.mu.RUnlock()
	host = strings.ToLower(host)
	for i := range m.sites {
		if strings.EqualFold(m.sites[i].Host, host) {
			return &m.sites[i]
		}
	}
	return nil
}

func (m *Manager) serveStatic(w http.ResponseWriter, r *http.Request, site *Site) {
	root := m.ResolveRoot(site.Root)
	if root == "" {
		http.Error(w, "site sem root configurado", http.StatusInternalServerError)
		return
	}
	if _, err := os.Stat(root); err != nil {
		http.Error(w, "raiz do site indisponível: "+root, http.StatusInternalServerError)
		return
	}
	m.serveSPA(w, r, root)
}

func (m *Manager) serveProxy(w http.ResponseWriter, r *http.Request, site *Site) {
	upstream := site.Upstream
	if upstream == "" {
		http.Error(w, "site proxy sem upstream", http.StatusInternalServerError)
		return
	}
	m.mu.Lock()
	proxy, ok := m.proxies[site.Host]
	if !ok {
		target, err := url.Parse(upstream)
		if err != nil {
			m.mu.Unlock()
			http.Error(w, "upstream inválido: "+err.Error(), http.StatusInternalServerError)
			return
		}
		proxy = httputil.NewSingleHostReverseProxy(target)
		m.proxies[site.Host] = proxy
	}
	m.mu.Unlock()

	// ajusta Host para o upstream
	r.Host = hostOnly(r.Host)
	proxy.ServeHTTP(w, r)
}

// serveSPA serve arquivos estáticos com fallback para index.html (rotas client-side).
func (m *Manager) serveSPA(w http.ResponseWriter, r *http.Request, root string) {
	fs := http.Dir(root)
	fsh := http.FileServer(fs)

	upath := r.URL.Path
	if upath == "/" {
		upath = "/index.html"
	}
	full := filepath.Join(root, filepath.Clean("/"+upath))
	if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
		if strings.HasSuffix(upath, ".html") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		fsh.ServeHTTP(w, r)
		return
	}

	idx := filepath.Join(root, "index.html")
	if _, err := os.Stat(idx); err == nil {
		r2 := new(http.Request)
		*r2 = *r
		r2.URL.Path = "/"
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		fsh.ServeHTTP(w, r2)
		return
	}
	http.NotFound(w, r)
}

func hostOnly(h string) string {
	// IPv6 com porta: [addr]:port
	if strings.HasPrefix(h, "[") {
		if i := strings.Index(h, "]"); i >= 0 {
			return h[1:i]
		}
		return strings.Trim(h, "[]")
	}
	// IPv4/domínio com porta
	if i := strings.LastIndex(h, ":"); i >= 0 {
		return h[:i]
	}
	return h
}

func isLocalHost(h string) bool {
	h = hostOnly(h)
	return h == "127.0.0.1" || h == "::1" || h == "localhost"
}

// isIPAddress verifica se o host é um endereço IP (IPv4 ou IPv6 sem colchetes).
func isIPAddress(host string) bool {
	host = strings.Trim(host, "[]")
	return net.ParseIP(host) != nil
}

func (m *Manager) findDefaultSite() *Site {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.sites {
		if m.sites[i].Enabled {
			return &m.sites[i]
		}
	}
	return nil
}

// WriteHostsFile gera data/sites.hosts com cada host -> bind do servidor web.
func (m *Manager) WriteHostsFile() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.writeHostsFileLocked()
}

func (m *Manager) writeHostsFileLocked() (string, error) {
	path, err := paths.SitesHostsFile()
	if err != nil {
		return "", err
	}
	addr := m.cfg.Web.Listen
	if addr == "" {
		addr = "127.0.0.1"
	}
	var b strings.Builder
	for _, s := range m.sites {
		if !s.Enabled {
			continue
		}
		h := strings.TrimSpace(s.Host)
		if h == "" {
			continue
		}
		fmt.Fprintf(&b, "%s %s\n", addr, h)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// SyncHosts recarrega sites do disco e reescreve o hosts file.
func (m *Manager) SyncHosts() error {
	if err := m.LoadSites(); err != nil {
		return err
	}
	_, err := m.WriteHostsFile()
	return err
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.loadSitesLocked(); err != nil {
		m.buf.Warnf("sites", "falha ao carregar sites: %v", err)
	}
	if _, err := m.writeHostsFileLocked(); err != nil {
		m.buf.Warnf("sites", "falha ao escrever hosts: %v", err)
	}

	port := m.cfg.Web.Port
	if port == 0 {
		port = 80
	}
	m.actualPort = port
	m.portFallback = false
	m.webRunning = false
	m.webError = ""

	go m.runWebServer(port)
	go m.startTLS()
	return nil
}

func (m *Manager) Restart() error {
	if err := m.Stop(); err != nil {
		m.buf.Warnf("sites", "parar servidor web: %v", err)
	}
	return m.Start()
}

func (m *Manager) runWebServer(port int) {
	if m.tryListen(port, false) {
		return
	}
	if port == 80 {
		m.tryListen(8080, true)
	}
}

func (m *Manager) listenAddrs(port int) []string {
	if m.cfg.Web.ExternalListen {
		return []string{
			fmt.Sprintf("[::]:%d", port),
			fmt.Sprintf("127.0.0.1:%d", port),
		}
	}
	listen := m.cfg.Web.Listen
	if listen == "" {
		listen = "127.0.0.1"
	}
	return []string{fmt.Sprintf("%s:%d", listen, port)}
}

func (m *Manager) tryListen(port int, fallback bool) bool {
	handler := m.Handler()
	for _, addr := range m.listenAddrs(port) {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			m.buf.Warnf("sites", "bind %s: %v", addr, err)
			continue
		}

		srv := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		}

		m.mu.Lock()
		m.srv = srv
		m.actualPort = port
		m.portFallback = fallback
		m.webRunning = true
		m.webError = ""
		m.mu.Unlock()

		if fallback {
			m.buf.Warnf("sites", "servidor web em %s (fallback — porta 80 indisponível)", addr)
		} else {
			m.buf.Infof("sites", "servidor web em %s", addr)
		}
		if _, err := m.WriteHostsFile(); err != nil {
			m.buf.Warnf("sites", "falha ao reescrever hosts: %v", err)
		}

		err = srv.Serve(ln)

		m.mu.Lock()
		m.webRunning = false
		if err != nil && err != http.ErrServerClosed {
			m.webError = err.Error()
			m.buf.Errorf("sites", "servidor web em %s: %v", addr, err)
		}
		m.mu.Unlock()
		return true
	}

	msg := fmt.Sprintf("não foi possível abrir a porta %d (ocupada ou sem permissão)", port)
	m.mu.Lock()
	m.webRunning = false
	m.webError = msg
	m.mu.Unlock()
	m.buf.Errorf("sites", "servidor web: %s", msg)
	return false
}

func (m *Manager) WebStatus() (running bool, actualPort int, fallback bool, errMsg string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.webRunning, m.actualPort, m.portFallback, m.webError
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	srv := m.srv
	srvTLS := m.srvTLS
	m.mu.Unlock()

	var first error
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if srv != nil {
		if err := srv.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
	}
	if srvTLS != nil {
		if err := srvTLS.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
	}
	m.mu.Lock()
	m.srv = nil
	m.srvTLS = nil
	m.webRunning = false
	m.tlsRunning = false
	m.mu.Unlock()
	return first
}

// ActualPort retorna a porta efetiva do servidor web (pode ser fallback 8080).
func (m *Manager) ActualPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.actualPort != 0 {
		return m.actualPort
	}
	if m.cfg.Web.Port != 0 {
		return m.cfg.Web.Port
	}
	return 80
}

func (m *Manager) PortFallback() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.portFallback
}

// ListSites expõe as definições carregadas para a API.
func (m *Manager) ListSites() []Site {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Site, len(m.sites))
	copy(out, m.sites)
	return out
}
