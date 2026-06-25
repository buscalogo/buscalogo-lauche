package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"buscalogo-agent/internal/autostart"
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/coredns"
	"buscalogo-agent/internal/couchdb"
	"buscalogo-agent/internal/dns"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/sites"
	"buscalogo-agent/internal/system"
	"buscalogo-agent/internal/tray"
	"buscalogo-agent/internal/yggdrasil"
	"buscalogo-agent/frontend"
)

type Server struct {
	cfg     *config.Config
	buf     *logx.Buffer
	coredns *coredns.Service
	ygg     *yggdrasil.Service
	couchdb *couchdb.Service
	dns     *dns.Manager
	sites   *sites.Manager
	srv     *http.Server
}

func New(cfg *config.Config, buf *logx.Buffer, cdns *coredns.Service, y *yggdrasil.Service, cdb *couchdb.Service, d *dns.Manager, sm *sites.Manager) *Server {
	s := &Server{cfg: cfg, buf: buf, coredns: cdns, ygg: y, couchdb: cdb, dns: d, sites: sm}
	mux := http.NewServeMux()
	s.routes(mux)
	s.srv = &http.Server{
		Addr:              cfg.API.Listen,
		Handler:           s.hostGuard(mux),
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
	mux.HandleFunc("GET /api/sites", s.handleSitesList)
	mux.HandleFunc("POST /api/sites", s.handleSitesAdd)
	mux.HandleFunc("DELETE /api/sites/{host}", s.handleSitesDelete)
	mux.HandleFunc("POST /api/web/enable-80", s.handleWebEnable80)
	mux.HandleFunc("GET /api/autostart", s.handleGetAutostart)
	mux.HandleFunc("POST /api/autostart/enable", s.handleAutostartEnable)
	mux.HandleFunc("POST /api/autostart/disable", s.handleAutostartDisable)
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
	Node      config.Node      `json:"node"`
	DNSMode   string           `json:"dns_mode"`
	Services  map[string]any   `json:"services"`
	System    dns.SystemInfo   `json:"system"`
	Web       webInfo          `json:"web"`
	Systray   tray.EnvInfo     `json:"systray"`
	Autostart bool             `json:"autostart"`
	Config    config.Data      `json:"config"`
}

type webInfo struct {
	Listen         string `json:"listen"`
	Port           int    `json:"port"`
	ActualPort     int    `json:"actual_port"`
	Fallback       bool   `json:"fallback"`
	ExternalListen bool   `json:"external_listen"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	actualPort := s.sites.ActualPort()
	resp := statusResp{
		Node:    s.cfg.Node,
		DNSMode: s.cfg.DNS.Mode,
		Services: map[string]any{
			"coredns":   s.coredns.Status(),
			"yggdrasil": s.ygg.Status(),
			"couchdb":   s.couchdb.Status(),
		},
		System:   dns.Detect(s.cfg),
		Web: webInfo{
			Listen:         s.cfg.Web.Listen,
			Port:           s.cfg.Web.Port,
			ActualPort:     actualPort,
			Fallback:       actualPort != s.cfg.Web.Port && s.cfg.Web.Port != 0,
			ExternalListen: s.cfg.Web.ExternalListen,
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
	default:
		writeErr(w, http.StatusBadRequest, "serviço desconhecido: %s", name)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%s/%s: %v", name, action, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "services": map[string]any{
		"coredns": s.coredns.Status(), "yggdrasil": s.ygg.Status(), "couchdb": s.couchdb.Status(),
	}})
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
	if err := s.cfg.MergeJSON(body); err != nil {
		writeErr(w, http.StatusBadRequest, "merge config: %v", err)
		return
	}
	if err := s.cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "save: %v", err)
		return
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
		"dns_mode":         s.cfg.DNS.Mode,
		"listen":           s.cfg.DNS.Listen,
		"port":             s.cfg.DNS.Port,
		"external_listen":  s.cfg.DNS.ExternalListen,
		"upstream":         s.cfg.DNS.Upstream,
		"search_domains":   s.cfg.DNS.SearchDomains,
		"yggdns":           s.cfg.Yggdrasil.DnsServers,
		"yggdns_enabled":   s.cfg.Yggdrasil.Enabled && len(s.cfg.Yggdrasil.DnsServers) > 0,
		"ygg_ip":           s.ygg.SelfAddress(),
		"corefile":         corefile,
		"system":           dns.Detect(s.cfg),
		"coredns_status":   s.coredns.Status(),
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

func (s *Server) handleWebEnable80(w http.ResponseWriter, r *http.Request) {
	exe, err := os.Executable()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "descobrir executável: %v", err)
		return
	}
	if err := system.SetCapNetBindService(s.buf, exe); err != nil {
		writeErr(w, http.StatusInternalServerError, "setcap cap_net_bind_service: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "cap_net_bind_service concedido ao agente. Reiniciando para usar a porta 80...",
	})
	go s.restartAgent(exe)
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

// restartAgent para serviços e reexecuta o binário com os mesmos argumentos.
// Usado após setcap para que o novo processo carregue a capability concedida.
func (s *Server) restartAgent(exe string) {
	s.buf.Infof("api", "reiniciando agente em 1s para carregar cap_net_bind_service")
	time.Sleep(1 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.coredns.Stop()
	_ = s.couchdb.Stop()
	_ = s.ygg.Stop()
	_ = s.sites.Stop()
	_ = s.Shutdown(ctx)
	// Re-executa o mesmo binário com os mesmos argumentos/ambiente.
	// O novo processo herdará as capabilities do arquivo executável.
	s.buf.Infof("api", "re-executando %s %v", exe, os.Args)
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		// Falha crítica: processo não pode continuar sem a capability.
		s.buf.Errorf("api", "falha ao re-executar agente: %v", err)
		os.Exit(1)
	}
}
