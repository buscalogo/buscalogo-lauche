package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"buscalogo-agent/frontend"
	"buscalogo-agent/internal/autostart"
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/coredns"
	"buscalogo-agent/internal/couchdb"
	"buscalogo-agent/internal/dns"
	"buscalogo-agent/internal/extension"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/openurl"
	"buscalogo-agent/internal/p2p"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/scraper"
	"buscalogo-agent/internal/sites"
	"buscalogo-agent/internal/system"
	"buscalogo-agent/internal/tray"
	"buscalogo-agent/internal/update"
	"buscalogo-agent/internal/version"
	"buscalogo-agent/internal/yggdrasil"
)

type Server struct {
	cfg     *config.Config
	buf     *logx.Buffer
	coredns *coredns.Service
	ygg     *yggdrasil.Service
	couchdb *couchdb.Service
	scraper *scraper.Service
	p2p     *p2p.Connector
	dns     *dns.Manager
	sites   *sites.Manager
	updater *update.Service
	srv     *http.Server
}

func New(cfg *config.Config, buf *logx.Buffer, cdns *coredns.Service, y *yggdrasil.Service, cdb *couchdb.Service, scr *scraper.Service, p2pConn *p2p.Connector, d *dns.Manager, sm *sites.Manager, upd *update.Service) *Server {
	s := &Server{cfg: cfg, buf: buf, coredns: cdns, ygg: y, couchdb: cdb, scraper: scr, p2p: p2pConn, dns: d, sites: sm, updater: upd}
	if upd != nil {
		upd.SetOnInstalled(func() {
			daemon, err := paths.DaemonExecutable()
			if err != nil {
				s.buf.Errorf("update", "daemon para reinício: %v", err)
				return
			}
			s.restartAgent(daemon)
		})
	}
	mux := http.NewServeMux()
	s.routes(mux)
	s.srv = &http.Server{
		Addr:              cfg.API.Listen,
		Handler:           s.hostGuard(s.corsLocal(mux)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// hostGuard rejeita requisições cujo Host header não corresponde ao endereço
// de escuta da API. Isso previne ataques de DNS rebinding, onde um site
// malicioso resolve seu domínio para 127.0.0.1 e faz o navegador do usuário
// atacar a API local.
func (s *Server) hostGuard(next http.Handler) http.Handler {
	allowed := make(map[string]bool)
	_, port, _ := strings.Cut(s.cfg.API.Listen, ":")
	for _, h := range []string{"127.0.0.1", "localhost", "::1"} {
		allowed[fmt.Sprintf("%s:%s", h, port)] = true
	}
	allowed[s.cfg.API.Listen] = true
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// corsLocal permite requisições do shell Neutralino (origem localhost em outra porta).
func (s *Server) corsLocal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); isLocalOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/service/{name}/{action}", s.handleService)
	mux.HandleFunc("GET /api/logs/recent", s.handleLogsRecent)
	mux.HandleFunc("GET /api/logs/stream", s.handleLogsStream)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)
	mux.HandleFunc("POST /api/dns/enable-system", s.handleDNSEnable)
	mux.HandleFunc("POST /api/dns/disable-system", s.handleDNSDisable)
	mux.HandleFunc("GET /api/dns/status", s.handleDNSStatus)
	mux.HandleFunc("POST /api/dns/upstream", s.handleDNSUpstreamAdd)
	mux.HandleFunc("DELETE /api/dns/upstream/{server}", s.handleDNSUpstreamRemove)
	mux.HandleFunc("POST /api/yggdrasil/setup-priv", s.handleYggPriv)
	mux.HandleFunc("GET /api/yggdrasil/info", s.handleYggInfo)
	mux.HandleFunc("GET /api/yggdrasil/identity", s.handleYggExportIdentity)
	mux.HandleFunc("POST /api/yggdrasil/identity", s.handleYggImportIdentity)
	mux.HandleFunc("GET /api/couchdb/info", s.handleCouchInfo)
	mux.HandleFunc("POST /api/couchdb/regenerate-password", s.handleCouchRegeneratePassword)
	mux.HandleFunc("POST /api/couchdb/repair", s.handleCouchRepair)
	mux.HandleFunc("GET /api/scraper/info", s.handleScraperInfo)
	mux.HandleFunc("GET /api/scraper/tasks", s.handleScraperTasks)
	mux.HandleFunc("POST /api/scraper/tasks", s.handleScraperAddTask)
	mux.HandleFunc("POST /api/scraper/batch", s.handleScraperBatch)
	mux.HandleFunc("GET /api/scraper/stats", s.handleScraperStats)
	mux.HandleFunc("GET /api/scraper/config", s.handleScraperGetConfig)
	mux.HandleFunc("POST /api/scraper/config", s.handleScraperSetConfig)
	mux.HandleFunc("POST /api/scraper/start", s.handleScraperStart)
	mux.HandleFunc("POST /api/scraper/stop", s.handleScraperStop)
	mux.HandleFunc("POST /api/scraper/clear", s.handleScraperClear)
	mux.HandleFunc("GET /api/scraper/results", s.handleScraperResults)
	mux.HandleFunc("DELETE /api/scraper/results/{docId}", s.handleScraperDeleteResult)
	mux.HandleFunc("GET /api/scraper/lookup", s.handleScraperLookup)
	mux.HandleFunc("GET /api/extension/info", s.handleExtensionInfo)
	mux.HandleFunc("POST /api/extension/open-dir", s.handleExtensionOpenDir)
	mux.HandleFunc("POST /api/extension/launch", s.handleExtensionLaunch)
	mux.HandleFunc("POST /api/extension/shortcut", s.handleExtensionShortcut)
	mux.HandleFunc("GET /api/p2p/status", s.handleP2PStatus)
	mux.HandleFunc("GET /api/p2p/test-search", s.handleP2PTestSearch)
	mux.HandleFunc("GET /api/p2p/config", s.handleP2PGetConfig)
	mux.HandleFunc("PUT /api/p2p/config", s.handleP2PPutConfig)
	mux.HandleFunc("POST /api/p2p/config/reset", s.handleP2PResetConfig)
	mux.HandleFunc("POST /api/p2p/restart", s.handleP2PRestart)
	mux.HandleFunc("GET /api/sites", s.handleSitesList)
	mux.HandleFunc("POST /api/sites", s.handleSitesAdd)
	mux.HandleFunc("DELETE /api/sites/{host}", s.handleSitesDelete)
	mux.HandleFunc("POST /api/open-url", s.handleOpenURL)
	mux.HandleFunc("POST /api/web/enable-80", s.handleWebEnable80)
	mux.HandleFunc("POST /api/web/restart", s.handleWebRestart)
	mux.HandleFunc("GET /api/autostart", s.handleGetAutostart)
	mux.HandleFunc("POST /api/autostart/enable", s.handleAutostartEnable)
	mux.HandleFunc("POST /api/autostart/disable", s.handleAutostartDisable)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/update/status", s.handleUpdateStatus)
	mux.HandleFunc("POST /api/update/check", s.handleUpdateCheck)
	mux.HandleFunc("POST /api/update/download", s.handleUpdateDownload)
	mux.HandleFunc("POST /api/update/install", s.handleUpdateInstall)
	mux.HandleFunc("POST /api/update/restart-app", s.handleUpdateRestartApp)
	mux.Handle("/", frontend.Handler())
}

func (s *Server) ListenAndServe() error {
	s.buf.Infof("api", "API ouvindo em http://%s", s.cfg.API.Listen)
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, format string, args ...any) {
	writeJSON(w, code, map[string]string{"error": fmt.Sprintf(format, args...)})
}

type statusResp struct {
	Node      config.Node    `json:"node"`
	Version   string         `json:"version"`
	DNSMode   string         `json:"dns_mode"`
	Services  map[string]any `json:"services"`
	System    dns.SystemInfo `json:"system"`
	Web       webInfo        `json:"web"`
	Systray   tray.EnvInfo   `json:"systray"`
	Autostart bool           `json:"autostart"`
	Config    config.Data    `json:"config"`
}

type webInfo struct {
	Listen         string `json:"listen"`
	Port           int    `json:"port"`
	ActualPort     int    `json:"actual_port"`
	Fallback       bool   `json:"fallback"`
	ExternalListen bool   `json:"external_listen"`
	Running        bool   `json:"running"`
	Error          string `json:"error,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	running, actualPort, fallback, webErr := s.sites.WebStatus()
	if actualPort == 0 {
		actualPort = s.sites.ActualPort()
	}
	if !running {
		fallback = s.sites.PortFallback()
	}
	resp := statusResp{
		Node:    s.cfg.Node,
		Version: version.Version,
		DNSMode: s.cfg.DNS.Mode,
		Services: map[string]any{
			"coredns":   s.coredns.Status(),
			"yggdrasil": s.ygg.Status(),
			"couchdb":   s.couchdb.Status(),
			"scraper":   s.scraper.Status(),
		},
		System: dns.Detect(s.cfg),
		Web: webInfo{
			Listen:         s.cfg.Web.Listen,
			Port:           s.cfg.Web.Port,
			ActualPort:     actualPort,
			Fallback:       fallback,
			ExternalListen: s.cfg.Web.ExternalListen,
			Running:        running,
			Error:          webErr,
		},
		Systray:   tray.CheckEnvironment(),
		Autostart: autostart.IsEnabled(),
		Config:    s.cfg.Snapshot(),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	action := r.PathValue("action")

	var err error
	switch name {
	case "coredns":
		err = s.applyAction(action, s.coredns.Start, s.coredns.Stop, s.coredns.Restart)
	case "yggdrasil":
		err = s.applyAction(action, s.ygg.Start, s.ygg.Stop, s.ygg.Restart)
	case "couchdb":
		err = s.applyAction(action, s.couchdb.Start, s.couchdb.Stop, s.couchdb.Restart)
	case "scraper":
		err = s.applyAction(action, s.scraper.Start, s.scraper.Stop, s.scraper.Restart)
	case "p2p":
		if s.p2p == nil {
			writeErr(w, http.StatusServiceUnavailable, "P2P não inicializado")
			return
		}
		err = s.applyAction(action, s.p2p.Start, s.p2p.Stop, s.p2p.Restart)
	default:
		writeErr(w, http.StatusBadRequest, "serviço desconhecido: %s", name)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%s/%s: %v", name, action, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "services": map[string]any{
		"coredns": s.coredns.Status(), "yggdrasil": s.ygg.Status(), "couchdb": s.couchdb.Status(), "scraper": s.scraper.Status(), "p2p": s.p2pStatusBrief(),
	}})
}

func (s *Server) handleScraperInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": s.scraper.Info()})
}

func (s *Server) handleScraperTasks(w http.ResponseWriter, r *http.Request) {
	active, queued := s.scraper.Engine().TasksSnapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"active": active,
			"queued": queued,
			"total":  len(active) + len(queued),
		},
	})
}

func (s *Server) handleScraperAddTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL            string `json:"url"`
		Priority       string `json:"priority"`
		DiscoveredFrom string `json:"discoveredFrom"`
		Depth          int    `json:"depth"`
		MaxDepth       int    `json:"maxDepth"`
		Type           string `json:"type"`
		ScheduleDays   int    `json:"scheduleDays"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeErr(w, http.StatusBadRequest, "URL é obrigatória")
		return
	}
	taskID, err := s.scraper.AddTask(body.URL, body.Priority, body.Depth, body.MaxDepth, body.ScheduleDays, body.DiscoveredFrom, body.Type)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"success": true,
		"message": "Tarefa adicionada à fila",
		"data": map[string]any{
			"taskId": taskID, "url": body.URL, "priority": body.Priority,
			"scheduleDays": body.ScheduleDays, "maxDepth": body.MaxDepth,
		},
	})
}

func (s *Server) handleScraperBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URLs         []string `json:"urls"`
		Priority     string   `json:"priority"`
		MaxDepth     int      `json:"maxDepth"`
		ScheduleDays int      `json:"scheduleDays"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	added := 0
	var ids []string
	for _, u := range body.URLs {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		id, err := s.scraper.AddTask(u, body.Priority, 0, body.MaxDepth, body.ScheduleDays, "", "batch")
		if err != nil {
			continue
		}
		if id != "" {
			added++
			ids = append(ids, id)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": fmt.Sprintf("%d URL(s) adicionada(s)", added),
		"data":    map[string]any{"taskIds": ids, "added": added},
	})
}

func (s *Server) handleScraperStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    s.scraper.Engine().Stats(),
	})
}

func (s *Server) handleScraperGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    s.scraper.ConfigMap(),
	})
}

func (s *Server) handleScraperSetConfig(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if err := s.scraper.ApplyConfig(body); err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    s.scraper.ConfigMap(),
	})
}

func (s *Server) handleScraperStart(w http.ResponseWriter, r *http.Request) {
	if err := s.scraper.Start(); err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Scraper iniciado"})
}

func (s *Server) handleScraperStop(w http.ResponseWriter, r *http.Request) {
	if err := s.scraper.Stop(); err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Scraper parado"})
}

func (s *Server) handleScraperClear(w http.ResponseWriter, r *http.Request) {
	s.scraper.Engine().ClearQueue()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Fila limpa"})
}

func (s *Server) handleScraperResults(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	results, err := s.scraper.ListResults(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    results,
		"total":   len(results),
	})
}

func (s *Server) handleScraperDeleteResult(w http.ResponseWriter, r *http.Request) {
	docID := r.PathValue("docId")
	if docID == "" {
		writeErr(w, http.StatusBadRequest, "docId obrigatório")
		return
	}
	if err := s.scraper.DeleteResult(docID); err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Resultado removido"})
}

func (s *Server) handleScraperLookup(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if raw == "" {
		writeErr(w, http.StatusBadRequest, "url é obrigatória")
		return
	}
	res, err := s.scraper.Lookup(raw)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    res,
	})
}

func (s *Server) handleExtensionInfo(w http.ResponseWriter, r *http.Request) {
	root := extensionInstallRoot()
	chromeDir := filepath.Join(root, "chrome")
	firefoxDir := filepath.Join(root, "firefox")
	chromeBin, chromeErr := extension.FindChromium()
	firefoxBin, firefoxErr := extension.FindFirefox()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"root": root,
		"chrome": map[string]any{
			"path":        chromeDir,
			"ready":       extension.DirReady(chromeDir),
			"browser_bin": chromeBin,
			"browser_ok":  chromeErr == nil,
			"one_click":   true,
			"install":     "chrome://extensions",
			"hint":        "Um clique abre o Chrome com a extensão já carregada (perfil BuscaLogo).",
		},
		"firefox": map[string]any{
			"path":        firefoxDir,
			"ready":       extension.DirReady(firefoxDir),
			"browser_bin": firefoxBin,
			"browser_ok":  firefoxErr == nil,
			"one_click":   false,
			"install":     "about:debugging#/runtime/this-firefox",
			"hint":        "Abre o Firefox, copia o caminho e a pasta — falta só “Carregar extensão temporária”.",
		},
	})
}

func (s *Server) handleExtensionOpenDir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Browser string `json:"browser"`
		OpenUI  bool   `json:"open_ui"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	browser := strings.ToLower(strings.TrimSpace(body.Browser))
	if browser == "" {
		browser = "chrome"
	}
	if browser != "chrome" && browser != "firefox" {
		writeErr(w, http.StatusBadRequest, "browser deve ser chrome ou firefox")
		return
	}
	dir := filepath.Join(extensionInstallRoot(), browser)
	if !extension.DirReady(dir) {
		writeErr(w, http.StatusNotFound, "extensão não encontrada em %s", dir)
		return
	}
	if err := openurl.OpenPath(dir); err != nil {
		writeErr(w, http.StatusInternalServerError, "abrir pasta: %v", err)
		return
	}
	uiURL := ""
	if body.OpenUI {
		switch browser {
		case "chrome":
			uiURL = "chrome://extensions"
		case "firefox":
			uiURL = "about:debugging#/runtime/this-firefox"
		}
		if uiURL != "" {
			if err := openurl.OpenBrowserPage(uiURL); err != nil {
				s.buf.Warnf("api", "abrir página de extensões: %v", err)
			}
		}
	}
	s.buf.Infof("api", "pasta da extensão aberta: %s", dir)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"path":   dir,
		"ui_url": uiURL,
	})
}

func (s *Server) handleExtensionLaunch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Browser string `json:"browser"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	browser := strings.ToLower(strings.TrimSpace(body.Browser))
	if browser == "" {
		browser = "chrome"
	}
	dir := filepath.Join(extensionInstallRoot(), browser)
	switch browser {
	case "chrome":
		bin, profile, err := extension.LaunchChrome(dir)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		s.buf.Infof("api", "Chrome lançado com extensão (%s, perfil=%s)", bin, profile)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"mode":    "load-extension",
			"browser": bin,
			"profile": profile,
			"path":    dir,
			"message": "Chrome aberto com a extensão BuscaLogo já ativa.",
		})
	case "firefox":
		bin, copied, err := extension.LaunchFirefoxGuide(dir)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		msg := "Firefox aberto em about:debugging. Clique em “Carregar extensão temporária” e escolha o manifest.json da pasta aberta."
		if copied {
			msg = "Caminho copiado para a área de transferência. No Firefox: Carregar extensão temporária → cole o caminho ou use a pasta aberta."
		}
		s.buf.Infof("api", "guia Firefox aberto (%s, clipboard=%v)", bin, copied)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"mode":      "guide",
			"browser":   bin,
			"path":      dir,
			"clipboard": copied,
			"message":   msg,
		})
	default:
		writeErr(w, http.StatusBadRequest, "browser deve ser chrome ou firefox")
	}
}

func (s *Server) handleExtensionShortcut(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(extensionInstallRoot(), "chrome")
	desktop, err := extension.EnsureChromeDesktopShortcut(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	s.buf.Infof("api", "atalho Chrome criado: %s", desktop)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"desktop": desktop,
		"message": "Atalho “BuscaLogo no Chrome” adicionado ao menu de aplicativos.",
	})
}

func extensionInstallRoot() string {
	candidates := []string{"/opt/buscalogo/extension"}
	if exe, err := os.Executable(); err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "extension"),
			filepath.Join(dir, "..", "extension"),
			filepath.Join(dir, "..", "exten"),
			filepath.Join(dir, "..", "..", "exten"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "extension"),
			filepath.Join(wd, "..", "exten"),
			filepath.Join(wd, "exten"),
		)
	}
	for _, c := range candidates {
		c = filepath.Clean(c)
		if extension.DirReady(filepath.Join(c, "chrome")) || extension.DirReady(filepath.Join(c, "firefox")) {
			return c
		}
	}
	return "/opt/buscalogo/extension"
}

func extensionDirReady(dir string) bool {
	return extension.DirReady(dir)
}

func (s *Server) handleP2PStatus(w http.ResponseWriter, r *http.Request) {
	if s.p2p == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"connected": false,
			"message":   "P2P não inicializado",
		})
		return
	}
	st := s.p2p.GetStats()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"connected": st.Connected,
		"message":   st.Message,
		"stats":     st,
		"config": map[string]any{
			"enabled":        s.cfg.P2PEnabled(),
			"signaling_urls": s.cfg.P2P.SignalingURLs,
			"max_results":    s.cfg.P2P.MaxResultsPerQuery,
		},
	})
}

func (s *Server) handleP2PGetConfig(w http.ResponseWriter, r *http.Request) {
	yamlText, err := s.cfg.P2PYAML()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	cfgPath := s.cfg.Path()
	if cfgPath == "" {
		if p, err := paths.ConfigFile(); err == nil {
			cfgPath = p
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"path": cfgPath,
		"yaml": yamlText,
		"p2p":  s.cfg.Snapshot().P2P,
	})
}

func (s *Server) handleP2PPutConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ler body: %v", err)
		return
	}
	var req struct {
		YAML string      `json:"yaml"`
		P2P  *config.P2P `json:"p2p"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido: %v", err)
		return
	}
	switch {
	case req.P2P != nil:
		if err := s.cfg.SetP2P(*req.P2P); err != nil {
			writeErr(w, http.StatusBadRequest, "%v", err)
			return
		}
	case strings.TrimSpace(req.YAML) != "":
		if err := s.cfg.ApplyP2PYAML(req.YAML); err != nil {
			writeErr(w, http.StatusBadRequest, "%v", err)
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "informe p2p ou yaml")
		return
	}
	s.restartP2PFromConfig()
	s.writeP2PConfigOK(w, "Configuração P2P salva")
}

func (s *Server) handleP2PResetConfig(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.ResetP2P(); err != nil {
		writeErr(w, http.StatusInternalServerError, "reset p2p: %v", err)
		return
	}
	s.restartP2PFromConfig()
	s.writeP2PConfigOK(w, "Configuração P2P restaurada ao padrão")
}

func (s *Server) restartP2PFromConfig() {
	if s.p2p == nil {
		return
	}
	go func() {
		if s.cfg.P2PEnabled() {
			if err := s.p2p.Restart(); err != nil {
				s.buf.Warnf("api", "reiniciar P2P após config: %v", err)
			}
		} else if err := s.p2p.Stop(); err != nil {
			s.buf.Warnf("api", "parar P2P após config: %v", err)
		}
	}()
}

func (s *Server) writeP2PConfigOK(w http.ResponseWriter, message string) {
	yamlText, _ := s.cfg.P2PYAML()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"yaml":    yamlText,
		"p2p":     s.cfg.Snapshot().P2P,
		"message": message,
	})
}

func (s *Server) handleP2PRestart(w http.ResponseWriter, r *http.Request) {
	if s.p2p == nil {
		writeErr(w, http.StatusServiceUnavailable, "P2P não inicializado")
		return
	}
	var err error
	if s.cfg.P2PEnabled() {
		err = s.p2p.Restart()
	} else {
		err = s.p2p.Stop()
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "reiniciar P2P: %v", err)
		return
	}
	st := s.p2p.GetStats()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"connected": st.Connected,
		"message":   st.Message,
		"stats":     st,
	})
}

func (s *Server) p2pStatusBrief() map[string]any {
	if s.p2p == nil {
		return map[string]any{"running": false, "connected": false}
	}
	st := s.p2p.GetStats()
	return map[string]any{
		"running":   st.Running,
		"connected": st.Connected,
		"message":   st.Message,
	}
}

func (s *Server) handleP2PTestSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "parâmetro q é obrigatório")
		return
	}
	if s.p2p == nil {
		writeErr(w, http.StatusServiceUnavailable, "P2P não inicializado")
		return
	}
	results, err := s.p2p.TestSearch(q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"query":   q,
		"count":   len(results),
		"results": results,
		"stats":   s.p2p.GetStats(),
	})
}

func (s *Server) handleCouchInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": s.couchdb.Info()})
}

func (s *Server) handleCouchRegeneratePassword(w http.ResponseWriter, r *http.Request) {
	user, pass, err := s.couchdb.RegenerateAdminPassword()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"admin_user":     user,
		"admin_password": pass,
		"info":           s.couchdb.Info(),
	})
}

func (s *Server) handleCouchRepair(w http.ResponseWriter, r *http.Request) {
	if err := s.couchdb.RepairAndStart(); err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"info":   s.couchdb.Info(),
		"status": s.couchdb.Status(),
	})
}

func (s *Server) applyAction(action string, start, stop, restart func() error) error {
	switch action {
	case "start":
		return start()
	case "stop":
		return stop()
	case "restart":
		return restart()
	default:
		return fmt.Errorf("ação inválida: %s", action)
	}
}

func (s *Server) handleLogsRecent(w http.ResponseWriter, r *http.Request) {
	n := 200
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	writeJSON(w, http.StatusOK, s.buf.Recent(n))
}

func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming não suportado")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	ch, unsub := s.buf.Subscribe(ctx)
	defer unsub()

	for _, e := range s.buf.Recent(50) {
		data, _ := json.Marshal(e)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.Snapshot())
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ler body: %v", err)
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido: %v", err)
		return
	}
	if err := s.cfg.MergeJSON(body); err != nil {
		writeErr(w, http.StatusBadRequest, "merge config: %v", err)
		return
	}
	if err := s.cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "save: %v", err)
		return
	}
	if _, hasP2P := raw["p2p"]; hasP2P && s.p2p != nil {
		go func() {
			if s.cfg.P2PEnabled() {
				if err := s.p2p.Restart(); err != nil {
					s.buf.Warnf("api", "reiniciar P2P após config: %v", err)
				}
			} else if err := s.p2p.Stop(); err != nil {
				s.buf.Warnf("api", "parar P2P após config: %v", err)
			}
		}()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": s.cfg.Snapshot()})
}

func (s *Server) handleDNSEnable(w http.ResponseWriter, r *http.Request) {
	if err := s.dns.EnableSystem(); err != nil {
		writeErr(w, http.StatusInternalServerError, "ativar DNS sistema: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dns_mode": s.cfg.DNS.Mode, "system": dns.Detect(s.cfg)})
}

func (s *Server) handleDNSDisable(w http.ResponseWriter, r *http.Request) {
	if err := s.dns.DisableSystem(); err != nil {
		writeErr(w, http.StatusInternalServerError, "desativar DNS sistema: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dns_mode": s.cfg.DNS.Mode, "system": dns.Detect(s.cfg)})
}

func (s *Server) handleDNSStatus(w http.ResponseWriter, r *http.Request) {
	var corefile string
	if path, err := s.coredns.CorefilePath(); err == nil {
		if data, err := os.ReadFile(path); err == nil {
			corefile = string(data)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dns_mode":        s.cfg.DNS.Mode,
		"listen":          s.cfg.DNS.Listen,
		"port":            s.cfg.DNS.Port,
		"external_listen": s.cfg.DNS.ExternalListen,
		"upstream":        s.cfg.DNS.Upstream,
		"search_domains":  s.cfg.DNS.SearchDomains,
		"yggdns":          s.cfg.Yggdrasil.DnsServers,
		"yggdns_enabled":  s.cfg.Yggdrasil.Enabled && len(s.cfg.Yggdrasil.DnsServers) > 0,
		"ygg_ip":          s.ygg.SelfAddress(),
		"corefile":        corefile,
		"system":          dns.Detect(s.cfg),
		"coredns_status":  s.coredns.Status(),
	})
}

func (s *Server) handleDNSUpstreamAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Server string `json:"server"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalido: %v", err)
		return
	}
	body.Server = strings.TrimSpace(body.Server)
	if body.Server == "" {
		writeErr(w, http.StatusBadRequest, "server obrigatorio")
		return
	}
	s.cfg.AddUpstream(body.Server)
	if err := s.cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "save: %v", err)
		return
	}
	if err := s.coredns.Restart(); err != nil {
		s.buf.Warnf("api", "restart coredns apos add upstream: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "upstream": s.cfg.DNS.Upstream})
}

func (s *Server) handleDNSUpstreamRemove(w http.ResponseWriter, r *http.Request) {
	server := r.PathValue("server")
	if server == "" {
		writeErr(w, http.StatusBadRequest, "server obrigatorio")
		return
	}
	removed := s.cfg.RemoveUpstream(server)
	if !removed {
		writeErr(w, http.StatusNotFound, "upstream %s nao encontrado", server)
		return
	}
	if err := s.cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "save: %v", err)
		return
	}
	if err := s.coredns.Restart(); err != nil {
		s.buf.Warnf("api", "restart coredns apos remover upstream: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "upstream": s.cfg.DNS.Upstream})
}

func (s *Server) handleYggPriv(w http.ResponseWriter, r *http.Request) {
	binary, err := s.ygg.BinaryPath()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "binário yggdrasil: %v", err)
		return
	}
	needCap := !system.HasCapContains(binary, "cap_net_admin")
	if needCap {
		if err := system.SetCapNet(s.buf, binary); err != nil {
			writeErr(w, http.StatusInternalServerError, "conceder privilégio TUN: %v", err)
			return
		}
		s.buf.Infof("api", "privilégio cap_net_admin concedido ao yggdrasil, reiniciando")
	} else {
		s.buf.Infof("api", "yggdrasil já possui cap_net_admin/cap_net_raw, reiniciando")
	}
	if err := s.ygg.Restart(); err != nil {
		writeErr(w, http.StatusInternalServerError, "reiniciar yggdrasil: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "yggdrasil": s.ygg.Status()})
}

func (s *Server) handleYggInfo(w http.ResponseWriter, r *http.Request) {
	info := s.ygg.AdminInfo()
	if info == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": map[string]any{"reachable": false}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": info})
}

func (s *Server) handleYggExportIdentity(w http.ResponseWriter, r *http.Request) {
	privKey, err := s.ygg.ExportIdentity()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "exportar identidade: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"private_key": privKey,
	})
}

func (s *Server) handleYggImportIdentity(w http.ResponseWriter, r *http.Request) {
	var p struct {
		PrivateKey string `json:"private_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido: %v", err)
		return
	}
	if p.PrivateKey == "" {
		writeErr(w, http.StatusBadRequest, "private_key é obrigatório")
		return
	}
	if err := s.ygg.ImportIdentity(p.PrivateKey); err != nil {
		writeErr(w, http.StatusInternalServerError, "importar identidade: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "yggdrasil": s.ygg.Status()})
}

func (s *Server) handleSitesList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sites": s.sites.ListSites()})
}

type sitePayload struct {
	Host     string `json:"host"`
	Type     string `json:"type"`
	Root     string `json:"root"`
	Upstream string `json:"upstream"`
	Enabled  bool   `json:"enabled"`
}

func (s *Server) handleSitesAdd(w http.ResponseWriter, r *http.Request) {
	var p sitePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido: %v", err)
		return
	}
	if p.Host == "" {
		writeErr(w, http.StatusBadRequest, "host é obrigatório")
		return
	}
	if p.Type == "" {
		p.Type = "static"
	}
	if p.Type == "static" && p.Root == "" {
		writeErr(w, http.StatusBadRequest, "root é obrigatório para sites estáticos")
		return
	}
	if p.Type == "proxy" && p.Upstream == "" {
		writeErr(w, http.StatusBadRequest, "upstream é obrigatório para sites proxy")
		return
	}
	// remove duplicado com mesmo host
	cur := s.cfg.Snapshot().Sites
	filtered := make([]config.Site, 0, len(cur)+1)
	for _, st := range cur {
		if st.Host != p.Host {
			filtered = append(filtered, st)
		}
	}
	filtered = append(filtered, config.Site{
		Host:     p.Host,
		Type:     p.Type,
		Root:     p.Root,
		Upstream: p.Upstream,
		Enabled:  p.Enabled,
	})
	s.cfg.SetSites(filtered)
	if err := s.cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "salvar config: %v", err)
		return
	}
	if err := s.sites.SyncHosts(); err != nil {
		s.buf.Warnf("api", "sync hosts: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sites": s.sites.ListSites()})
}

func (s *Server) handleSitesDelete(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if host == "" {
		writeErr(w, http.StatusBadRequest, "host é obrigatório na URL")
		return
	}
	cur := s.cfg.Snapshot().Sites
	filtered := make([]config.Site, 0, len(cur))
	for _, st := range cur {
		if st.Host != host {
			filtered = append(filtered, st)
		}
	}
	s.cfg.SetSites(filtered)
	if err := s.cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "salvar config: %v", err)
		return
	}
	if err := s.sites.SyncHosts(); err != nil {
		s.buf.Warnf("api", "sync hosts: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sites": s.sites.ListSites()})
}

func (s *Server) handleOpenURL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
		writeErr(w, http.StatusBadRequest, "url é obrigatória")
		return
	}
	if err := openurl.Open(body.URL); err != nil {
		writeErr(w, http.StatusBadRequest, "abrir url: %v", err)
		return
	}
	s.buf.Infof("api", "abrindo no navegador: %s", body.URL)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWebEnable80(w http.ResponseWriter, r *http.Request) {
	daemon, err := paths.DaemonExecutable()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "descobrir executável: %v", err)
		return
	}
	if err := system.SetCapNetBindService(s.buf, daemon); err != nil {
		writeErr(w, http.StatusInternalServerError, "setcap cap_net_bind_service: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "cap_net_bind_service concedido ao agente. Reiniciando para usar a porta 80...",
	})
	go s.restartAgent(daemon)
}

func (s *Server) handleWebRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.sites.Restart(); err != nil {
		writeErr(w, http.StatusInternalServerError, "reiniciar servidor web: %v", err)
		return
	}
	time.Sleep(150 * time.Millisecond)
	running, port, fallback, webErr := s.sites.WebStatus()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"running":     running,
		"actual_port": port,
		"fallback":    fallback,
		"error":       webErr,
	})
}

func (s *Server) handleGetAutostart(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"enabled": autostart.IsEnabled(),
	})
}

func (s *Server) handleAutostartEnable(w http.ResponseWriter, r *http.Request) {
	if err := autostart.Enable(s.buf); err != nil {
		writeErr(w, http.StatusInternalServerError, "ativar autostart: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": true})
}

func (s *Server) handleAutostartDisable(w http.ResponseWriter, r *http.Request) {
	if err := autostart.Disable(s.buf); err != nil {
		writeErr(w, http.StatusInternalServerError, "desativar autostart: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": false})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	go s.stopAllAndExit()
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, version.Info())
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "atualizações não disponíveis")
		return
	}
	writeJSON(w, http.StatusOK, s.updater.Status())
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "atualizações não disponíveis")
		return
	}
	st, err := s.updater.Check(true)
	if err != nil {
		writeJSON(w, http.StatusOK, st)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleUpdateDownload(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "atualizações não disponíveis")
		return
	}
	st, err := s.updater.Download()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "atualizações não disponíveis")
		return
	}
	st, err := s.updater.Install()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleUpdateRestartApp(w http.ResponseWriter, r *http.Request) {
	if s.updater != nil {
		s.updater.ClearNeedsRestart()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "relaunch": true})
}

func (s *Server) stopAllAndExit() {
	time.Sleep(100 * time.Millisecond)
	s.buf.Infof("api", "encerrando serviços (solicitação de shutdown)")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = s.Shutdown(ctx)
	_ = s.sites.Stop()
	_ = s.coredns.Stop()
	_ = s.couchdb.Stop()
	_ = s.scraper.Stop()
	_ = s.ygg.Stop()
	s.buf.Infof("api", "encerrado")
	os.Exit(0)
}

// restartAgent reinicia o daemon após setcap ou atualização .deb.
func (s *Server) restartAgent(daemon string) {
	s.buf.Infof("api", "reiniciando agente após atualização")
	if err := scheduleDaemonRestart(daemon, s.buf); err != nil {
		s.buf.Errorf("api", "agendar reinício: %v — tentando exec direto", err)
		s.stopServicesForRestart()
		time.Sleep(6 * time.Second)
		args := daemonArgs(daemon)
		env := append(os.Environ(), "BUSCALOGO_POST_UPDATE=1")
		if err := syscall.Exec(daemon, args, env); err != nil {
			s.buf.Errorf("api", "falha ao re-executar agente: %v", err)
			os.Exit(1)
		}
	}
	os.Exit(0)
}

func (s *Server) stopServicesForRestart() {
	if s.p2p != nil {
		s.p2p.Stop()
	}
	_ = s.sites.Stop()
	_ = s.coredns.Stop()
	_ = s.couchdb.Stop()
	_ = s.scraper.Stop()
	_ = s.ygg.Stop()
}

func daemonArgs(daemon string) []string {
	base := filepath.Base(daemon)
	if base == "buscalogo-agentd" {
		return []string{daemon, "--no-tray"}
	}
	if len(os.Args) > 0 && os.Args[0] == daemon {
		return os.Args
	}
	return append([]string{daemon}, os.Args[1:]...)
}
