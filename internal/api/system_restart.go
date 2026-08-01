package api

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/process"
)

// handleSystemRestartNetwork reinicia Ygg + DNS + sites + gossip sem matar o agentd.
func (s *Server) handleSystemRestartNetwork(w http.ResponseWriter, r *http.Request) {
	s.buf.Infof("api", "reinício de rede solicitado (Ygg/DNS/sites/gossip)")
	steps := s.restartNetworkStack()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"scope": "network",
		"steps": steps,
	})
}

// handleSystemRestartAll para tudo, limpa órfãos e reinicia o agentd.
func (s *Server) handleSystemRestartAll(w http.ResponseWriter, r *http.Request) {
	daemon, err := paths.DaemonExecutable()
	if err != nil || daemon == "" {
		writeErr(w, http.StatusInternalServerError, "binário do agentd não encontrado: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"scope":  "all",
		"hint":   "O painel vai cair por alguns segundos; reabra em http://127.0.0.1:9970",
		"daemon": daemon,
	})
	go s.fullSystemRestart(daemon)
}

func (s *Server) fullSystemRestart(daemon string) {
	time.Sleep(200 * time.Millisecond)
	s.buf.Infof("api", "reinício completo do Agent — limpando serviços e órfãos")
	s.stopServicesForRestart()
	if s.p2pdomain != nil {
		_ = s.p2pdomain.Stop()
	}
	s.killOrphanServices()
	if err := scheduleDaemonRestart(daemon, s.buf); err != nil {
		s.buf.Errorf("api", "agendar reinício: %v — tentando exec direto", err)
		time.Sleep(3 * time.Second)
		args := daemonArgs(daemon)
		env := append(os.Environ(), "BUSCALOGO_POST_UPDATE=1")
		if err := reexecDaemon(daemon, args, env); err != nil {
			s.buf.Errorf("api", "falha ao re-executar agente: %v", err)
			os.Exit(1)
		}
	}
	os.Exit(0)
}

func (s *Server) restartNetworkStack() []string {
	var steps []string
	add := func(msg string) {
		steps = append(steps, msg)
		s.buf.Infof("api", "%s", msg)
	}

	if s.p2pdomain != nil {
		_ = s.p2pdomain.Stop()
		add("gossip parado")
	}
	if s.sites != nil {
		_ = s.sites.Stop()
		add("sites HTTP/HTTPS parados")
	}
	if s.coredns != nil {
		_ = s.coredns.Stop()
		add("CoreDNS parado")
	}
	if s.ygg != nil {
		_ = s.ygg.Stop()
		add("Yggdrasil parado")
	}
	s.killOrphanServices()
	add("órfãos limpos")
	time.Sleep(800 * time.Millisecond)

	if s.ygg != nil && s.cfg.Yggdrasil.Enabled {
		if err := s.ygg.Start(); err != nil {
			add("Yggdrasil: " + err.Error())
		} else {
			add("Yggdrasil iniciado")
			time.Sleep(1500 * time.Millisecond)
		}
	}
	if s.coredns != nil && s.cfg.DNS.Enabled {
		if err := s.coredns.Start(); err != nil {
			add("CoreDNS: " + err.Error())
		} else {
			add("CoreDNS iniciado")
		}
		if s.dns != nil {
			s.dns.EnsureSystemIntegration()
		}
	}
	if s.sites != nil {
		if err := s.sites.Start(); err != nil {
			add("sites: " + err.Error())
		} else {
			add("sites iniciados")
		}
	}
	if s.p2pdomain != nil && s.cfg.Yggdrasil.Enabled && s.ygg != nil {
		ip := s.ygg.SelfAddressReady()
		if ip == "" {
			for i := 0; i < 15 && ip == ""; i++ {
				time.Sleep(time.Second)
				ip = s.ygg.SelfAddressReady()
			}
		}
		if ip != "" {
			if err := s.p2pdomain.Start(context.Background(), ip, nil, s.cfg.Registry.BootstrapPeers); err != nil {
				add("gossip: " + err.Error())
			} else {
				res := s.p2pdomain.SyncNow(context.Background())
				add("gossip iniciado (sync connected=" + strconv.Itoa(res.Connected) + ")")
			}
		} else {
			add("gossip adiado — Ygg IPv6 indisponível")
		}
	}
	return steps
}

func (s *Server) killOrphanServices() {
	if s.ygg != nil {
		if bin, err := s.ygg.BinaryPath(); err == nil && bin != "" {
			_ = process.KillExistingByBinary(s.buf, "yggdrasil", bin)
		}
	}
	if s.coredns != nil {
		if bin, err := s.coredns.BinaryPath(); err == nil && bin != "" {
			_ = process.KillExistingByBinary(s.buf, "coredns", bin)
		}
	}
}
