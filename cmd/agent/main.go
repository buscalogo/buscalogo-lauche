package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"buscalogo-agent/internal/account"
	"buscalogo-agent/internal/api"
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/coredns"
	"buscalogo-agent/internal/couchdb"
	"buscalogo-agent/internal/dns"
	"buscalogo-agent/internal/firewall"
	"buscalogo-agent/internal/ledger"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/p2p"
	"buscalogo-agent/internal/p2pdomain"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/process"
	"buscalogo-agent/internal/scraper"
	"buscalogo-agent/internal/sites"
	"buscalogo-agent/internal/store"
	"buscalogo-agent/internal/tray"
	"buscalogo-agent/internal/update"
	"buscalogo-agent/internal/winsvc"
	"buscalogo-agent/internal/yggdrasil"
)

func main() {
	log.SetFlags(log.Ltime)
	noTray := flag.Bool("no-tray", false, "executa sem systray (modo headless)")
	asService := flag.Bool("service", false, "executa como serviço Windows (SCM)")
	trayUI := flag.Bool("tray-ui", false, "só bandeja/UI — não inicia Ygg/DNS (espera o serviço)")
	flag.Parse()

	if *trayUI {
		runTrayUIOnly()
		return
	}

	if *asService {
		if err := winsvc.Run(winsvc.ServiceName, func(stop <-chan struct{}) error {
			return runAgent(stop, true)
		}); err != nil {
			log.Fatalf("serviço: %v", err)
		}
		return
	}

	stop := make(chan struct{})
	if *noTray {
		go func() {
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
			<-sigs
			close(stop)
		}()
		if err := runAgent(stop, true); err != nil {
			log.Fatalf("agent: %v", err)
		}
		return
	}

	if err := runAgent(stop, false); err != nil {
		log.Fatalf("agent: %v", err)
	}
}

func runTrayUIOnly() {
	cfg, err := config.Load()
	panelURL := "http://127.0.0.1:9970"
	if err == nil && cfg.API.Listen != "" {
		panelURL = "http://" + cfg.API.Listen
	}
	buf := logx.NewBuffer(200)
	buf.Infof("agent", "modo tray-ui (painel %s)", panelURL)
	tray.RunUIOnly(panelURL, buf)
}

// runAgent inicia o stack completo. Quando headless=true, bloqueia até stop fechar.
// Quando headless=false, corre o systray e encerra ao sair do tray.
func runAgent(stop <-chan struct{}, headless bool) error {
	home, err := paths.Home()
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
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
	scrapeBackend := "CouchDB/buscalogo_scraping"
	if runtime.GOOS == "windows" {
		scrapeBackend = "SQLite/data/scrape/index.sqlite"
	}
	log.Printf("CouchDB: http://%s:%d (modo=%s enabled=%v)", couchListen, couchPort, cfg.CouchDB.Mode, cfg.CouchDB.Enabled)
	log.Printf("Scraper: nativo Go → %s (enabled=%v)", scrapeBackend, cfg.Scraper.Enabled)
	log.Printf("P2P busca: %d signaling(s) (enabled=%v)", len(cfg.P2P.SignalingURLs), cfg.P2PEnabled())

	buf.Infof("agent", "BuscaLogo Agent iniciando (home=%s)", home)
	buf.Infof("agent", "API painel: http://%s", cfg.API.Listen)
	buf.Infof("agent", "DNS resolver: %s:%d (modo=%s)", cfg.DNS.Listen, cfg.DNS.Port, cfg.DNS.Mode)
	buf.Infof("agent", "Web sites: %s:%d (fallback 8080 se sem permissão)", cfg.Web.Listen, webPort)
	buf.Infof("agent", "Yggdrasil modo=%s", cfg.Yggdrasil.Mode)
	buf.Infof("agent", "CouchDB: http://%s:%d (modo=%s enabled=%v)", couchListen, couchPort, cfg.CouchDB.Mode, cfg.CouchDB.Enabled)
	buf.Infof("agent", "Scraper: nativo Go → %s (enabled=%v)", scrapeBackend, cfg.Scraper.Enabled)
	buf.Infof("agent", "P2P busca: %d signaling(s) enabled=%v", len(cfg.P2P.SignalingURLs), cfg.P2PEnabled())

	postUpdate := os.Getenv("BUSCALOGO_POST_UPDATE") == "1"
	if postUpdate {
		_ = os.Unsetenv("BUSCALOGO_POST_UPDATE")
		buf.Infof("agent", "reinício pós-atualização — limpando processos órfãos")
		cleanupStaleProcesses(buf)
		time.Sleep(2 * time.Second)
	}

	if runtime.GOOS == "windows" {
		if err := firewall.EnsureBLInboundRules(buf, 4401, webPort, 443); err != nil {
			buf.Warnf("agent", "firewall: %v", err)
		}
	}

	cdns := coredns.New(cfg, buf)
	ygg := yggdrasil.New(cfg, buf)
	cdb := couchdb.New(cfg, buf)
	scr := scraper.New(cfg, cdb, buf)
	acct := account.New(cdb, buf)
	scrapeStore, err := scraper.OpenScrapeStore(cdb, buf)
	if err != nil {
		buf.Errorf("agent", "índice de scrape: %v", err)
	}
	if scrapeStore != nil {
		scrapeStore.SetSigner(acct)
		scr.SetStore(scrapeStore)
		scr.SetSigner(acct)
	}
	p2pConn := p2p.New(cfg, scrapeStore, buf)
	dnsMgr := dns.NewManager(cfg, buf, cdns)
	sitesMgr := sites.New(cfg, buf)
	updater := update.New(cfg, buf)

	var ledgerEng *ledger.Engine
	var domainGossip *p2pdomain.Service
	var regStore store.Store
	if cfg.RegistryEnabled() {
		st, err := store.Open(cfg.Registry.Engine, cfg.Registry.Path)
		if err != nil {
			buf.Errorf("agent", "registry store: %v", err)
		} else {
			regStore = st
			ledgerEng = ledger.NewEngine(st, buf)
			ledgerEng.SetOnHostsWrite(func(path string) {
				buf.Infof("ledger", "hosts atualizado: %s", path)
				if cfg.DNS.Enabled {
					_, _ = cdns.WriteCorefile()
				}
			})
			if _, err := st.WriteHostsFile(); err != nil {
				buf.Warnf("agent", "registry hosts: %v", err)
			}
			domainGossip = p2pdomain.New(ledgerEng, buf, cfg.Registry.GossipTopic, cfg.Registry.ListenPort)
			domainGossip.SetYggPeersFn(func() []string {
				return ygg.PeerAddresses()
			})
			domainGossip.SetStaticPeers(cfg.Registry.StaticPeers)
			if len(cfg.Registry.StaticPeers) > 0 {
				buf.Infof("agent", "registry static_peers: %v", cfg.Registry.StaticPeers)
			} else if config.DefaultRegistryYggIP != "" {
				buf.Infof("agent", "DefaultRegistryYggIP=%s (sem static_peers na config)", config.DefaultRegistryYggIP)
			}
		}
	}

	srv := api.New(cfg, buf, cdns, ygg, cdb, scr, acct, p2pConn, dnsMgr, sitesMgr, updater, ledgerEng, domainGossip)
	updater.StartBackground()

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
	if cfg.Yggdrasil.Enabled {
		time.Sleep(1500 * time.Millisecond)
	}
	if domainGossip != nil && cfg.Yggdrasil.Enabled {
		go func() {
			for i := 0; i < 120; i++ {
				ip := ygg.SelfAddressReady()
				if ip == "" {
					time.Sleep(time.Second)
					continue
				}
				if err := domainGossip.Start(context.Background(), ip, nil, cfg.Registry.BootstrapPeers); err != nil {
					buf.Warnf("agent", "p2pdomain: %v (tentativa %d)", err, i+1)
					time.Sleep(time.Second)
					continue
				}
				time.Sleep(3 * time.Second)
				res := domainGossip.SyncNow(context.Background())
				buf.Infof("agent", "p2pdomain sync inicial: connected=%d applied=%d tried=%d peers=%d",
					res.Connected, res.Applied, res.Tried, res.Peers)
				return
			}
			buf.Warnf("agent", "p2pdomain: Ygg IPv6/TUN ainda indisponível — gossip adiado (verifique wintun.dll / Admin)")
		}()
	}
	startService("CoreDNS", cfg.DNS.Enabled, cdns.Start)
	if cfg.DNS.Enabled {
		time.Sleep(500 * time.Millisecond)
		dnsMgr.EnsureSystemIntegration()
	}
	startService("CouchDB", cfg.CouchDB.Enabled, cdb.Start)
	if cfg.CouchDB.Enabled {
		cdb.StartWatchdog()
		time.Sleep(2 * time.Second)
	}
	if _, err := acct.EnsureServerID(); err != nil {
		buf.Warnf("agent", "server_id da conta: %v", err)
	} else {
		buf.Infof("agent", "conta storage=%s", acct.StorageBackend())
	}
	go acct.RestoreSessionRetry(15, time.Second)
	startService("Scraper", cfg.Scraper.Enabled, scr.Start)
	startService("P2P", cfg.P2PEnabled(), p2pConn.Start)
	if err := sitesMgr.Start(); err != nil {
		buf.Errorf("agent", "servidor de sites: %v", err)
	}

	if postUpdate {
		go func() {
			time.Sleep(8 * time.Second)
			buf.Infof("agent", "verificação pós-atualização dos serviços")
			if cfg.CouchDB.Enabled && cdb.Status().State != "running" {
				buf.Warnf("agent", "CouchDB não rodando após update — reparando")
				_ = cdb.RepairAndStart()
			} else if cfg.CouchDB.Enabled && !cdb.Reachable(nil) {
				buf.Warnf("agent", "CouchDB não responde após update — reparando")
				_ = cdb.RepairAndStart()
			}
			if cfg.DNS.Enabled && cdns.Status().State != "running" {
				buf.Warnf("agent", "CoreDNS não rodando após update — reiniciando")
				_ = cdns.Restart()
			}
			if cfg.Yggdrasil.Enabled && ygg.Status().State != "running" {
				buf.Warnf("agent", "Yggdrasil não rodando após update — reiniciando")
				_ = ygg.Restart()
			}
			running, _, _, _ := sitesMgr.WebStatus()
			if !running {
				buf.Warnf("agent", "servidor web parado após update — reiniciando")
				_ = sitesMgr.Stop()
				if err := sitesMgr.Start(); err != nil {
					buf.Errorf("agent", "servidor de sites após update: %v", err)
				}
			}
			if cfg.P2PEnabled() && !p2pConn.GetStats().Connected {
				buf.Warnf("agent", "P2P desconectado após update — reconectando")
				_ = p2pConn.Stop()
				_ = p2pConn.Start()
			}
		}()
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			buf.Errorf("agent", "API: %v", err)
		}
	}()

	panelURL := "http://" + cfg.API.Listen
	log.Printf("painel web pronto em %s", panelURL)
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
		if domainGossip != nil {
			_ = domainGossip.Stop()
		}
		if regStore != nil {
			_ = regStore.Close()
		}
		_ = ygg.Stop()
		buf.Infof("agent", "encerrado")
	}

	if headless {
		buf.Infof("agent", "modo headless (sem systray)")
		<-stop
		shutdown()
		return nil
	}

	env := tray.CheckEnvironment()
	log.Printf("systray ambiente: desktop=%s ok=%v", env.Desktop, env.OK)
	buf.Infof("agent", "systray ambiente: desktop=%s ok=%v", env.Desktop, env.OK)
	if !env.OK {
		log.Printf("AVISO systray: %s", env.Warning)
		buf.Warnf("agent", "systray pode não aparecer: %s", env.Warning)
	}

	tray.New(panelURL, buf, cfg, cdns, ygg, cdb, scr, sitesMgr, nil).Run()
	shutdown()
	return nil
}

func cleanupStaleProcesses(buf *logx.Buffer) {
	targets := []struct {
		name string
		bin  string
	}{
		{"coredns", "/opt/buscalogo/data/bin/coredns"},
		{"yggdrasil", "/opt/buscalogo/data/bin/yggdrasil"},
		{"couchdb", ""},
		{"beam.smp", ""},
		{"epmd", ""},
	}
	for _, t := range targets {
		if err := process.KillExistingByBinary(buf, t.name, t.bin); err != nil {
			buf.Warnf("agent", "limpeza %s: %v", t.name, err)
		}
	}
}
