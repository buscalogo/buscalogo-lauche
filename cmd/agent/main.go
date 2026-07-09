package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"buscalogo-agent/internal/api"
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/coredns"
	"buscalogo-agent/internal/couchdb"
	"buscalogo-agent/internal/dns"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/p2p"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/scraper"
	"buscalogo-agent/internal/sites"
	"buscalogo-agent/internal/tray"
	"buscalogo-agent/internal/update"
	"buscalogo-agent/internal/yggdrasil"
)

func main() {
	log.SetFlags(log.Ltime)
	noTray := flag.Bool("no-tray", false, "executa sem systray (modo headless)")
	flag.Parse()

	home, err := paths.Home()
	if err != nil {
		log.Fatalf("home: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	buf := logx.NewBuffer(cfg.Cache.Size)
	log.Printf("BuscaLogo Agent iniciando (home=%s)", home)
	log.Printf("API painel: http://%s", cfg.API.Listen)
	log.Printf("DNS resolver: %s:%d (modo=%s)", cfg.DNS.Listen, cfg.DNS.Port, cfg.DNS.Mode)
	webPort := cfg.Web.Port
	if webPort == 0 {
		webPort = 80
	}
	log.Printf("Web sites: %s:%d (fallback 8080 se sem permissão)", cfg.Web.Listen, webPort)
	log.Printf("Yggdrasil modo=%s", cfg.Yggdrasil.Mode)
	couchPort := cfg.CouchDB.Port
	if couchPort == 0 {
		couchPort = 5984
	}
	couchListen := cfg.CouchDB.Listen
	if couchListen == "" {
		couchListen = "127.0.0.1"
	}
	log.Printf("CouchDB: http://%s:%d (modo=%s)", couchListen, couchPort, cfg.CouchDB.Mode)
	log.Printf("Scraper: nativo Go → CouchDB/%s (enabled=%v)", "buscalogo_scraping", cfg.Scraper.Enabled)
	log.Printf("P2P busca: %d signaling(s) (enabled=%v)", len(cfg.P2P.SignalingURLs), cfg.P2PEnabled())

	buf.Infof("agent", "BuscaLogo Agent iniciando (home=%s)", home)
	buf.Infof("agent", "API painel: http://%s", cfg.API.Listen)
	buf.Infof("agent", "DNS resolver: %s:%d (modo=%s)", cfg.DNS.Listen, cfg.DNS.Port, cfg.DNS.Mode)
	buf.Infof("agent", "Web sites: %s:%d (fallback 8080 se sem permissão)", cfg.Web.Listen, webPort)
	buf.Infof("agent", "Yggdrasil modo=%s", cfg.Yggdrasil.Mode)
	buf.Infof("agent", "CouchDB: http://%s:%d (modo=%s)", couchListen, couchPort, cfg.CouchDB.Mode)
	buf.Infof("agent", "Scraper: nativo Go → CouchDB/buscalogo_scraping (enabled=%v)", cfg.Scraper.Enabled)
	buf.Infof("agent", "P2P busca: %d signaling(s) enabled=%v", len(cfg.P2P.SignalingURLs), cfg.P2PEnabled())

	cdns := coredns.New(cfg, buf)
	ygg := yggdrasil.New(cfg, buf)
	cdb := couchdb.New(cfg, buf)
	scr := scraper.New(cfg, cdb, buf)
	var scrapeStore *scraper.Store
	if cdb != nil {
		scrapeStore = scraper.NewStore(cdb)
	}
	p2pConn := p2p.New(cfg, scrapeStore, buf)
	dnsMgr := dns.NewManager(cfg, buf, cdns)
	sitesMgr := sites.New(cfg, buf)
	updater := update.New(cfg, buf)
	srv := api.New(cfg, buf, cdns, ygg, cdb, scr, p2pConn, dnsMgr, sitesMgr, updater)
	updater.StartBackground()

	// Garante o hosts file dos sites ANTES de gerar o Corefile do CoreDNS.
	if err := sitesMgr.SyncHosts(); err != nil {
		buf.Warnf("agent", "escrever sites.hosts: %v", err)
	}

	startService := func(name string, enabled bool, start func() error) {
		if !enabled {
			buf.Infof("agent", "%s desabilitado na config", name)
			return
		}
		if err := start(); err != nil {
			buf.Errorf("agent", "falha ao iniciar %s: %v", name, err)
		}
	}
	startService("Yggdrasil", cfg.Yggdrasil.Enabled, ygg.Start)
	startService("CoreDNS", cfg.DNS.Enabled, cdns.Start)
	startService("CouchDB", cfg.CouchDB.Enabled, cdb.Start)
	startService("Scraper", cfg.Scraper.Enabled, scr.Start)
	startService("P2P", cfg.P2PEnabled(), p2pConn.Start)
	if err := sitesMgr.Start(); err != nil {
		buf.Errorf("agent", "servidor de sites: %v", err)
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			buf.Errorf("agent", "API: %v", err)
		}
	}()

	panelURL := "http://" + cfg.API.Listen
	log.Printf("painel web pronto em %s", panelURL)
	log.Printf("acesse via navegador: %s", panelURL)
	buf.Infof("agent", "painel web em %s", panelURL)

	shutdown := func() {
		buf.Infof("agent", "encerrando serviços...")
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = sitesMgr.Stop()
		_ = cdns.Stop()
		_ = cdb.Stop()
		_ = scr.Stop()
		_ = p2pConn.Stop()
		_ = ygg.Stop()
		buf.Infof("agent", "encerrado")
	}

	if *noTray {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		buf.Infof("agent", "modo headless (sem systray)")
		<-sigs
		shutdown()
		return
	}

	env := tray.CheckEnvironment()
	log.Printf("systray ambiente: desktop=%s ok=%v", env.Desktop, env.OK)
	buf.Infof("agent", "systray ambiente: desktop=%s ok=%v", env.Desktop, env.OK)
	if !env.OK {
		log.Printf("AVISO systray: %s", env.Warning)
		log.Printf("systray ajuda: %s", env.Details)
		buf.Warnf("agent", "systray pode não aparecer: %s", env.Warning)
		buf.Warnf("agent", "systray ajuda: %s", env.Details)
	}

	tray.New(panelURL, buf, cfg, cdns, ygg, cdb, scr, sitesMgr, nil).Run()
	shutdown()
}
