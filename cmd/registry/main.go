package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"buscalogo-agent/internal/api"
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/coredns"
	"buscalogo-agent/internal/dns"
	"buscalogo-agent/internal/ledger"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/p2pdomain"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/sites"
	"buscalogo-agent/internal/store"
	"buscalogo-agent/internal/yggdrasil"
)

func main() {
	log.SetFlags(log.Ltime)

	home, err := paths.Home()
	if err != nil {
		log.Fatalf("home: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := cfg.ApplyRegistryNodeDefaults(); err != nil {
		log.Fatalf("defaults registry-node: %v", err)
	}

	buf := logx.NewBuffer(cfg.Cache.Size)
	buf.SetEcho(true) // PM2 / stdout vê sync e rede
	log.Printf("BuscaLogo Registry Node (home=%s)", home)
	buf.Infof("registry", "Registry Node iniciando (home=%s)", home)
	buf.Infof("registry", "API: http://%s", cfg.API.Listen)
	buf.Infof("registry", "modo: Ygg + ledger/gossip + sites + DNS (sem scraper/CouchDB/P2P busca)")
	buf.Infof("registry", "logs: pm2 logs | curl http://%s/api/logs/recent?n=50 | SSE /api/logs/stream", cfg.API.Listen)

	cdns := coredns.New(cfg, buf)
	ygg := yggdrasil.New(cfg, buf)

	// Garante binário Ygg embutido/extraído ANTES de subir a mesh.
	yggBin, err := ygg.BinaryPath()
	if err != nil {
		log.Fatalf("Yggdrasil binário: %v (rode 'make assets' em buscalogo-lauche)", err)
	}
	buf.Infof("registry", "Yggdrasil binary: %s", yggBin)
	log.Printf("Yggdrasil será iniciado — no 1º boot gera identidade e IPv6 estável (grave em ygg.ip para os Agents)")

	dnsMgr := dns.NewManager(cfg, buf, cdns)
	sitesMgr := sites.New(cfg, buf)

	var ledgerEng *ledger.Engine
	var domainGossip *p2pdomain.Service
	var regStore store.Store
	st, err := store.Open(cfg.Registry.Engine, cfg.Registry.Path)
	if err != nil {
		log.Fatalf("registry store: %v", err)
	}
	regStore = st
	ledgerEng = ledger.NewEngine(st, buf)
	ledgerEng.SetOnHostsWrite(func(path string) {
		buf.Infof("ledger", "hosts atualizado: %s", path)
		if cfg.DNS.Enabled {
			_, _ = cdns.WriteCorefile()
		}
	})
	if _, err := st.WriteHostsFile(); err != nil {
		buf.Warnf("registry", "hosts: %v", err)
	}
	domainGossip = p2pdomain.New(ledgerEng, buf, cfg.Registry.GossipTopic, cfg.Registry.ListenPort)
	domainGossip.SetYggPeersFn(func() []string {
		return ygg.PeerAddresses()
	})
	domainGossip.SetStaticPeers(cfg.Registry.StaticPeers)

	// API completa com serviços opcionais nil (sem scraper/couch/account/p2p/update).
	srv := api.New(cfg, buf, cdns, ygg, nil, nil, nil, nil, dnsMgr, sitesMgr, nil, ledgerEng, domainGossip)

	if err := sitesMgr.SyncHosts(); err != nil {
		buf.Warnf("registry", "sites.hosts: %v", err)
	}

	start := func(name string, enabled bool, fn func() error) {
		if !enabled {
			buf.Infof("registry", "%s desabilitado", name)
			return
		}
		if err := fn(); err != nil {
			buf.Errorf("registry", "falha %s: %v", name, err)
		}
	}

	start("Yggdrasil", cfg.Yggdrasil.Enabled, ygg.Start)
	if cfg.Yggdrasil.Enabled {
		time.Sleep(1500 * time.Millisecond)
	}

	if cfg.Yggdrasil.Enabled {
		go func() {
			for i := 0; i < 40; i++ {
				ip := ygg.SelfAddress()
				if ip == "" {
					time.Sleep(500 * time.Millisecond)
					continue
				}
				if err := domainGossip.Start(context.Background(), ip, nil, cfg.Registry.BootstrapPeers); err != nil {
					buf.Errorf("registry", "p2pdomain: %v", err)
					return
				}
				writeBootstrapFile(buf, ygg, domainGossip)
				time.Sleep(2 * time.Second)
				res := domainGossip.SyncNow(context.Background())
				buf.Infof("registry", "sync inicial: connected=%d applied=%d tried=%d peers=%d",
					res.Connected, res.Applied, res.Tried, res.Peers)
				// Reescreve bootstrap após gossip ter addrs.
				writeBootstrapFile(buf, ygg, domainGossip)
				return
			}
			buf.Warnf("registry", "Ygg IPv6 indisponível — bootstrap adiado")
		}()
	}

	start("CoreDNS", cfg.DNS.Enabled, cdns.Start)
	if err := sitesMgr.Start(); err != nil {
		buf.Errorf("registry", "sites: %v", err)
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			buf.Errorf("registry", "API: %v", err)
		}
	}()

	panel := "http://" + cfg.API.Listen
	log.Printf("registry pronto — painel %s", panel)
	log.Printf("bootstrap: cat $BUSCALOGO_HOME/data/registry-bootstrap.txt")
	buf.Infof("registry", "painel %s", panel)

	go statusTicker(buf, ygg, domainGossip, ledgerEng)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	buf.Infof("registry", "encerrando...")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = sitesMgr.Stop()
	_ = cdns.Stop()
	_ = domainGossip.Stop()
	_ = regStore.Close()
	_ = ygg.Stop()
	buf.Infof("registry", "encerrado")
}

// statusTicker resume a rede a cada minuto (visível no PM2).
func statusTicker(buf *logx.Buffer, ygg *yggdrasil.Service, gossip *p2pdomain.Service, eng *ledger.Engine) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		ip := ygg.SelfAddress()
		st := map[string]any{}
		if gossip != nil {
			st = gossip.Status()
		}
		domains := 0
		if eng != nil {
			if list, err := eng.Store().ListDNS(); err == nil {
				domains = len(list)
			}
		}
		buf.Infof("registry", "rede: ygg=%s peers=%v known=%v serve_events=%v sync_events=%v domains=%d last_serve=%v",
			ip,
			st["peers"], st["known_agents"], st["serve_events"], st["sync_events"],
			domains, st["last_serve"])
	}
}

func writeBootstrapFile(buf *logx.Buffer, ygg *yggdrasil.Service, gossip *p2pdomain.Service) {
	dataDir, err := paths.Data()
	if err != nil {
		return
	}
	ip := ygg.SelfAddress()
	if ip == "" {
		return
	}
	port := 4401
	discover := port + 1
	st := map[string]any{}
	if gossip != nil {
		st = gossip.Status()
		if p, ok := st["port"].(int); ok && p > 0 {
			port = p
			discover = port + 1
		}
	}
	var addrs string
	if raw, ok := st["addrs"].([]string); ok && len(raw) > 0 {
		for _, a := range raw {
			addrs += a + "\n"
		}
	}
	peerID, _ := st["peer_id"].(string)
	body := fmt.Sprintf(`# BuscaLogo Registry bootstrap
# Compile Agents with:
#   go build -ldflags "-X buscalogo-agent/internal/config.DefaultRegistryYggIP=%s" ./cmd/agent
#
YGG_IP=%s
DISCOVER_PORT=%d
LIBP2P_PORT=%d
PEER_ID=%s
ADDRS=
%s
`, ip, ip, discover, port, peerID, addrs)

	path := filepath.Join(dataDir, "registry-bootstrap.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		buf.Warnf("registry", "bootstrap file: %v", err)
		return
	}
	buf.Infof("registry", "bootstrap escrito: %s (Ygg=%s)", path, ip)
	log.Printf("REGISTRY BOOTSTRAP Ygg IPv6 = %s (discover :%d)", ip, discover)
}
