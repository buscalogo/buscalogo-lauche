package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"buscalogo-agent/internal/paths"
	"gopkg.in/yaml.v3"
)

// Config é a configuração viva (com lock) do agente.
type Config struct {
	mu   sync.RWMutex
	path string
	Data `yaml:",inline"`
}

// Data contém apenas os campos serializáveis (sem lock). Pode ser copiada/serializada.
type Data struct {
	Node      Node      `yaml:"node" json:"node"`
	API       API       `yaml:"api" json:"api"`
	Web       Web       `yaml:"web" json:"web"`
	DNS       DNS       `yaml:"dns" json:"dns"`
	Yggdrasil Yggdrasil `yaml:"yggdrasil" json:"yggdrasil"`
	CouchDB   CouchDB   `yaml:"couchdb" json:"couchdb"`
	Sites     []Site    `yaml:"sites" json:"sites"`
	Scraper   Scraper   `yaml:"scraper" json:"scraper"`
	P2P       P2P       `yaml:"p2p" json:"p2p"`
	Registry  Registry  `yaml:"registry" json:"registry"`
	Update    Update    `yaml:"update" json:"update"`
	Cache     Cache     `yaml:"cache" json:"cache"`
	Bootstrap []string  `yaml:"bootstrap" json:"bootstrap"`
}

type Node struct {
	Name string `yaml:"name" json:"name"`
}

type API struct {
	Listen string `yaml:"listen" json:"listen"`
}

// Web é o servidor de hospedagem de sites (.bl).
type Web struct {
	Listen         string `yaml:"listen" json:"listen"`
	Port           int    `yaml:"port" json:"port"`
	ExternalListen bool   `yaml:"external_listen" json:"external_listen"` // escutar em todas as interfaces (inclui Yggdrasil)
	TLS            WebTLS `yaml:"tls" json:"tls"`
}

// WebTLS configura HTTPS (:443). Certs em cert_dir; modo self_signed até existir CA BuscaLogo.
type WebTLS struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	Port     int    `yaml:"port" json:"port"`           // default 443
	CertDir  string `yaml:"cert_dir" json:"cert_dir"`   // relativo a data/ ou absoluto
	Mode     string `yaml:"mode" json:"mode"`           // self_signed | files | ca (futuro)
	CertFile string `yaml:"cert_file" json:"cert_file"` // opcional (mode=files)
	KeyFile  string `yaml:"key_file" json:"key_file"`
}

// Site mapeia um host .bl a um diretório no disco (static) ou a um upstream (proxy).
type Site struct {
	Host     string `yaml:"host" json:"host"`
	Type     string `yaml:"type" json:"type"`         // static | proxy
	Root     string `yaml:"root" json:"root"`         // para type=static
	Upstream string `yaml:"upstream" json:"upstream"` // para type=proxy, ex: http://127.0.0.1:3000
	Enabled  bool   `yaml:"enabled" json:"enabled"`
}

type DNS struct {
	Enabled       bool     `yaml:"enabled" json:"enabled"`
	Mode          string   `yaml:"mode" json:"mode"` // local | system
	Listen        string   `yaml:"listen" json:"listen"`
	Port          int      `yaml:"port" json:"port"`
	Upstream      []string `yaml:"upstream" json:"upstream"`
	SearchDomains []string `yaml:"search_domains" json:"search_domains"` // TLDs do BuscaLogo (ex: bl)
	ExternalListen bool   `yaml:"external_listen" json:"external_listen"` // escutar em todas as interfaces (inclui Yggdrasil)
}

type Yggdrasil struct {
	Enabled        bool     `yaml:"enabled" json:"enabled"`
	Mode           string   `yaml:"mode" json:"mode"` // own | external
	ExternalBinary string   `yaml:"external_binary" json:"external_binary"`
	Peers          []string `yaml:"peers" json:"peers"`
	DnsServers     []string `yaml:"dns_servers" json:"dns_servers"` // resolvedores DNS na rede Yggdrasil
}

// Registry é o ledger .bl (Badger/SQLite + GossipSub sobre Ygg).
type Registry struct {
	Enabled        *bool    `yaml:"enabled" json:"enabled"`
	Engine         string   `yaml:"engine" json:"engine"` // badger | sqlite
	Path           string   `yaml:"path" json:"path"`
	GossipTopic    string   `yaml:"gossip_topic" json:"gossip_topic"`
	ListenPort     int      `yaml:"listen_port" json:"listen_port"`
	BootstrapPeers []string `yaml:"bootstrap_peers" json:"bootstrap_peers"`
	// StaticPeers são IPv6 Ygg de outros Agents (funciona entre redes diferentes).
	StaticPeers []string `yaml:"static_peers" json:"static_peers"`
}

func (r Registry) EnabledOrDefault() bool {
	if r.Enabled == nil {
		return true
	}
	return *r.Enabled
}

// DefaultCouchDBDatabases são os bancos padrão (compatível com bl-scraper-server).
var DefaultCouchDBDatabases = []string{
	"buscalogo_main",
	"buscalogo_config",
	"buscalogo_scraping",
	"buscalogo_p2p",
}

// CouchDB configura o servidor de documentos local (índice, curadoria, sync).
type CouchDB struct {
	Enabled       bool     `yaml:"enabled" json:"enabled"`
	Mode          string   `yaml:"mode" json:"mode"` // own | external
	ExternalPath  string   `yaml:"external_path" json:"external_path"`
	Listen        string   `yaml:"listen" json:"listen"`
	Port          int      `yaml:"port" json:"port"`
	AdminUser     string   `yaml:"admin_user" json:"admin_user"`
	AdminPassword string   `yaml:"admin_password" json:"admin_password"`
	Databases     []string `yaml:"databases" json:"databases"`
}

// DefaultPeers são peers públicos estáveis da rede Yggdrasil global.
// Selecionados por uptime 100% e proximidade geográfica ao Brasil.
var DefaultPeers = []string{
	"quic://ip6.casa2.mywire.org:44443?key=000000003cb1cc50e05147fc548f6d1f78e7ffcdc67b456f9bb0db6f0a5e4723",
	"tcp://ygg-1.okade.pro:20000",
	"tls://51.15.204.214:54321",
	"tls://yggdrasil.neilalexander.dev:64648?key=ecbbcb3298e7d3b4196103333c3e839cfe47a6ca47602b94a6d596683f6bb358",
}

// DefaultYggdns são resolvedores DNS públicos na rede Yggdrasil.
// Fonte: https://yggdrasil-network.github.io/services.html
var DefaultYggdns = []string{
	"324:71e:281a:9ed3::53",   // acetone — Alfis, Meshname, OpenNIC
	"302:db60::53",            // Revertron, Praha — Alfis, AdGuard
	"300:6223::53",            // Revertron, Bratislava — Alfis, AdGuard
	"302:7991::53",            // Revertron, St. Petersburg — Alfis, AdGuard
	"202:1d4e:724e:de52:8273:e2b5:4988:a9ba", // strcat.su — non-filtering
}

type Scraper struct {
	Enabled              bool     `yaml:"enabled" json:"enabled"`
	MaxConcurrent        int      `yaml:"max_concurrent" json:"max_concurrent"`
	MaxDepth             int      `yaml:"max_depth" json:"max_depth"`
	MaxRetries           int      `yaml:"max_retries" json:"max_retries"`
	RequestDelayMs       int      `yaml:"request_delay_ms" json:"request_delay_ms"`
	MaxLinksPerPage      int      `yaml:"max_links_per_page" json:"max_links_per_page"`
	DiscoverInternalOnly bool     `yaml:"discover_internal_only" json:"discover_internal_only"`
	DefaultScheduleDays  int      `yaml:"default_schedule_days" json:"default_schedule_days"`
	BlockedDomains       []string `yaml:"blocked_domains" json:"blocked_domains"`
	AllowedDomains       []string `yaml:"allowed_domains" json:"allowed_domains"`
}

// P2P conecta o agente à rede de busca BuscaLogo (signaling WebSocket).
type P2P struct {
	Enabled            *bool    `yaml:"enabled,omitempty" json:"enabled"`
	SignalingURLs      []string `yaml:"signaling_urls" json:"signaling_urls"`
	MaxResultsPerQuery int      `yaml:"max_results_per_query" json:"max_results_per_query"`
}

// EnabledOrDefault retorna true quando enabled não está definido no YAML (padrão: conectado).
func (p P2P) EnabledOrDefault() bool {
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

func BoolPtr(v bool) *bool {
	return &v
}

type Cache struct {
	Size int `yaml:"size" json:"size"`
}

// Update configura verificação de novas versões via GitHub Releases.
type Update struct {
	Enabled            *bool  `yaml:"enabled,omitempty" json:"enabled"`
	CheckIntervalHours int    `yaml:"check_interval_hours" json:"check_interval_hours"`
	GitHubRepo         string `yaml:"github_repo" json:"github_repo"`
	Channel            string `yaml:"channel" json:"channel"`
}

func (u Update) EnabledOrDefault() bool {
	if u.Enabled == nil {
		return true
	}
	return *u.Enabled
}

func (u Update) CheckIntervalHoursOrDefault() int {
	if u.CheckIntervalHours <= 0 {
		return 24
	}
	return u.CheckIntervalHours
}

func (u Update) GitHubRepoOrDefault() string {
	if strings.TrimSpace(u.GitHubRepo) == "" {
		return "buscalogo/buscalogo-lauche"
	}
	return strings.TrimSpace(u.GitHubRepo)
}

func DefaultUpdate() Update {
	return Update{
		Enabled:            BoolPtr(true),
		CheckIntervalHours: 24,
		GitHubRepo:         "buscalogo/buscalogo-lauche",
		Channel:            "stable",
	}
}

func Default() *Config {
	return &Config{Data: Data{
		Node: Node{Name: "BuscaLogo Node"},
		API:  API{Listen: "127.0.0.1:9970"},
		Web: Web{Listen: "127.0.0.1", Port: 80, ExternalListen: true, TLS: WebTLS{
			Enabled: true,
			Port:    443,
			Mode:    "self_signed",
		}},
		DNS: DNS{
			Enabled:       true,
			Mode:          "local",
			Listen:        "127.0.0.1",
			Port:          5333,
			Upstream:      []string{"1.1.1.1", "8.8.8.8"},
			SearchDomains: []string{"bl"},
			ExternalListen: false,
		},
		Yggdrasil: Yggdrasil{
			Enabled: true,
			Mode:    "own",
			Peers:   DefaultPeers,
		},
		CouchDB: CouchDB{
			Enabled:   platformDefaultCouchEnabled(),
			Mode:      "own",
			Listen:    "127.0.0.1",
			Port:      5984,
			Databases: DefaultCouchDBDatabases,
		},
		Sites: []Site{
			{Host: "buscalogo.bl", Type: "static", Root: defaultBuscaLogoDist(), Enabled: true},
		},
		Scraper: Scraper{
			Enabled:              false,
			MaxConcurrent:        3,
			MaxDepth:             3,
			MaxRetries:           3,
			RequestDelayMs:       2000,
			MaxLinksPerPage:      100,
			DiscoverInternalOnly: true,
			DefaultScheduleDays:  7,
		},
		P2P: DefaultP2P(),
		Registry: DefaultRegistry(),
		Update:  DefaultUpdate(),
		Cache:   Cache{Size: 2048},
	}}
}

func defaultBuscaLogoDist() string {
	if v := os.Getenv("BUSCALOGO_SITE_ROOT"); v != "" {
		if fi, err := os.Stat(v); err == nil && fi.IsDir() {
			return v
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	exeDir := filepath.Dir(exe)
	// Se instalado em diretório de sistema (ex: /opt/buscalogo), www fica junto ao binário.
	// Se rodando de pasta portátil, procura também em caminhos relativos.
	candidates := []string{
		filepath.Join(exeDir, "www", "buscalogo.com"),
		filepath.Join(exeDir, "..", "www", "buscalogo.com"),
		filepath.Join(exeDir, "..", "buscalogo.com", "dist"),
		filepath.Join(exeDir, "..", "..", "buscalogo.com", "dist"),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return ""
}

func Load() (*Config, error) {
	p, err := paths.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("resolver arquivo de config: %w", err)
	}
	return loadAtPath(p)
}

func loadAtPath(p string) (*Config, error) {
	c := Default()
	c.path = p

	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		if err := c.Save(); err != nil {
			return nil, fmt.Errorf("criar config inicial: %w", err)
		}
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler config: %w", err)
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	c.path = p
	return c, nil
}

func (c *Config) applyDefaults() {
	if c.API.Listen == "" {
		c.API.Listen = "127.0.0.1:9970"
	}
	if c.Web.Listen == "" {
		c.Web.Listen = "127.0.0.1"
	}
	if c.Web.Port == 0 {
		c.Web.Port = 80
	}
	if c.Web.TLS.Port == 0 {
		c.Web.TLS.Port = 443
	}
	if c.Web.TLS.Mode == "" {
		// Config legada sem bloco tls → liga HTTPS self-signed (:443).
		c.Web.TLS.Mode = "self_signed"
		c.Web.TLS.Enabled = true
	}
	if c.DNS.Mode == "" {
		c.DNS.Mode = "local"
	}
	if platformForceDNSLocal() && c.DNS.Mode == "system" {
		c.DNS.Mode = "local"
	}
	if c.DNS.Listen == "" {
		c.DNS.Listen = "127.0.0.1"
	}
	if c.DNS.Port == 0 {
		c.DNS.Port = 5333
	}
	if len(c.DNS.Upstream) == 0 {
		c.DNS.Upstream = []string{"1.1.1.1", "8.8.8.8"}
	}
	if len(c.DNS.SearchDomains) == 0 {
		c.DNS.SearchDomains = []string{"bl"}
	}
	if c.Yggdrasil.Mode == "" {
		c.Yggdrasil.Mode = "own"
	}
	if len(c.Yggdrasil.Peers) == 0 {
		c.Yggdrasil.Peers = DefaultPeers
	}
	if len(c.Yggdrasil.DnsServers) == 0 {
		c.Yggdrasil.DnsServers = DefaultYggdns
	}
	if c.CouchDB.Mode == "" {
		c.CouchDB.Mode = "own"
	}
	// Windows: sem CouchDB embutido (~108MB) — desliga por padrão (use mode=external se tiver).
	if runtime.GOOS == "windows" && c.CouchDB.Mode == "own" {
		c.CouchDB.Enabled = false
	}
	if c.CouchDB.Listen == "" {
		c.CouchDB.Listen = "127.0.0.1"
	}
	if c.CouchDB.Port == 0 {
		c.CouchDB.Port = 5984
	}
	if len(c.CouchDB.Databases) == 0 {
		c.CouchDB.Databases = DefaultCouchDBDatabases
	}
	if c.Cache.Size == 0 {
		c.Cache.Size = 2048
	}
	if len(c.P2P.SignalingURLs) == 0 {
		c.P2P.SignalingURLs = []string{"wss://api.buscalogo.com"}
	}
	if c.P2P.MaxResultsPerQuery <= 0 {
		c.P2P.MaxResultsPerQuery = 50
	}
	applyRegistryDefaults(&c.Registry)
}

// DefaultRegistryYggIP — seed público do ledger .bl (registry).
// Pode sobrescrever em compile-time:
//
//	go build -ldflags "-X buscalogo-agent/internal/config.DefaultRegistryYggIP=200:..."
//
// Agents usam este IPv6 como static peer quando registry.static_peers está vazio.
var DefaultRegistryYggIP = "200:63ac:4c32:e7f3:4db2:9c6e:6f3d:d088"

func applyRegistryDefaults(r *Registry) {
	if r.Engine == "" {
		r.Engine = "badger"
	}
	if r.GossipTopic == "" {
		r.GossipTopic = "/buscalogo/bl/v1"
	}
	if r.ListenPort == 0 {
		r.ListenPort = 4401
	}
	// Bootstrap compile-time: só injeta se o usuário ainda não configurou peers.
	if len(r.StaticPeers) == 0 && strings.TrimSpace(DefaultRegistryYggIP) != "" {
		ip := strings.TrimSpace(DefaultRegistryYggIP)
		ip = strings.Trim(ip, "[]")
		r.StaticPeers = []string{ip}
	}
}

// ApplyRegistryNodeDefaults força o perfil do nó seed público (sem scraper/Couch/P2P busca).
func (c *Config) ApplyRegistryNodeDefaults() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	falseV := false
	trueV := true
	c.Scraper.Enabled = false
	c.CouchDB.Enabled = false
	c.P2P.Enabled = &falseV
	c.Registry.Enabled = &trueV
	c.Yggdrasil.Enabled = true
	c.DNS.Enabled = true
	c.Web.ExternalListen = true
	if c.Web.Listen == "" || c.Web.Listen == "127.0.0.1" {
		c.Web.Listen = "0.0.0.0"
	}
	if c.API.Listen == "" || strings.HasPrefix(c.API.Listen, "127.0.0.1:") {
		port := "9970"
		if _, p, ok := strings.Cut(c.API.Listen, ":"); ok && p != "" {
			port = p
		}
		c.API.Listen = "0.0.0.0:" + port
	}
	if c.Node.Name == "" || c.Node.Name == "buscalogo" {
		c.Node.Name = "buscalogo-registry"
	}
	c.Update.Enabled = &falseV
	applyRegistryDefaults(&c.Registry)
	return nil
}

// DefaultRegistry retorna a configuração padrão do ledger .bl.
func DefaultRegistry() Registry {
	en := true
	return Registry{
		Enabled:     &en,
		Engine:      "badger",
		GossipTopic: "/buscalogo/bl/v1",
		ListenPort:  4401,
	}
}

// RegistryEnabled indica se o ledger .bl deve iniciar.
func (c *Config) RegistryEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Registry.EnabledOrDefault()
}

// P2PEnabled indica se o P2P deve iniciar (padrão: true).
func (c *Config) P2PEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.P2P.EnabledOrDefault()
}

func applyP2PDefaults(p *P2P) {
	if len(p.SignalingURLs) == 0 {
		p.SignalingURLs = []string{"wss://api.buscalogo.com"}
	}
	if p.MaxResultsPerQuery <= 0 {
		p.MaxResultsPerQuery = 50
	}
}

// P2PYAML retorna o bloco p2p do config.yaml como texto YAML.
func (c *Config) P2PYAML() (string, error) {
	c.mu.RLock()
	p := P2P{
		Enabled:            BoolPtr(c.P2P.EnabledOrDefault()),
		SignalingURLs:      append([]string(nil), c.P2P.SignalingURLs...),
		MaxResultsPerQuery: c.P2P.MaxResultsPerQuery,
	}
	c.mu.RUnlock()
	data, err := yaml.Marshal(&p)
	if err != nil {
		return "", fmt.Errorf("serializar p2p: %w", err)
	}
	return string(data), nil
}

// EnsureWebExternalListen liga web.external_listen para sites .bl na mesh.
func (c *Config) EnsureWebExternalListen() error {
	c.mu.Lock()
	if c.Web.ExternalListen {
		c.mu.Unlock()
		return nil
	}
	c.Web.ExternalListen = true
	c.mu.Unlock()
	return c.Save()
}

// SetP2P atualiza a seção p2p e persiste no config.yaml.
func (c *Config) SetP2P(p P2P) error {
	applyP2PDefaults(&p)
	c.mu.Lock()
	c.P2P = p
	c.mu.Unlock()
	return c.Save()
}

// DefaultP2P retorna a configuração P2P padrão do agente.
func DefaultP2P() P2P {
	return P2P{
		Enabled:            BoolPtr(true),
		SignalingURLs:      []string{"wss://api.buscalogo.com"},
		MaxResultsPerQuery: 50,
	}
}

// ResetP2P restaura a seção p2p aos valores padrão.
func (c *Config) ResetP2P() error {
	return c.SetP2P(DefaultP2P())
}

// ApplyP2PYAML interpreta YAML da seção p2p e persiste.
func (c *Config) ApplyP2PYAML(text string) error {
	var p P2P
	if err := yaml.Unmarshal([]byte(text), &p); err != nil {
		return fmt.Errorf("yaml inválido: %w", err)
	}
	return c.SetP2P(p)
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.path == "" {
		p, err := paths.ConfigFile()
		if err != nil {
			return err
		}
		c.path = p
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("serializar config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o644)
}

func (c *Config) Path() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.path
}

func (c *Config) Snapshot() Data {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked()
}

func (c *Config) snapshotLocked() Data {
	sites := make([]Site, len(c.Sites))
	copy(sites, c.Sites)
	return Data{
		Node:      c.Node,
		API:       c.API,
		Web:       c.Web,
		DNS:       c.DNS,
		Yggdrasil: c.Yggdrasil,
		CouchDB:   c.CouchDB,
		Sites:     sites,
		Scraper:   c.Scraper,
		P2P: P2P{
			Enabled:            BoolPtr(c.P2P.EnabledOrDefault()),
			SignalingURLs:      append([]string(nil), c.P2P.SignalingURLs...),
			MaxResultsPerQuery: c.P2P.MaxResultsPerQuery,
		},
		Update:    c.Update,
		Cache:     c.Cache,
		Bootstrap: append([]string(nil), c.Bootstrap...),
	}
}

// SetSites substitui a lista de sites de forma thread-safe.
func (c *Config) SetSites(sites []Site) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Sites = sites
}

// RemoveSite remove um host da lista de sites de forma thread-safe.
// Retorna true se o host foi encontrado e removido.
func (c *Config) RemoveSite(host string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	filtered := make([]Site, 0, len(c.Sites))
	found := false
	for _, s := range c.Sites {
		if s.Host == host {
			found = true
			continue
		}
		filtered = append(filtered, s)
	}
	if found {
		c.Sites = filtered
	}
	return found
}

// AddUpstream adiciona um servidor DNS upstream se ainda nao existir.
func (c *Config) AddUpstream(server string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, u := range c.DNS.Upstream {
		if u == server {
			return
		}
	}
	c.DNS.Upstream = append(c.DNS.Upstream, server)
}

// RemoveUpstream remove um servidor DNS upstream. Retorna true se removido.
func (c *Config) RemoveUpstream(server string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, u := range c.DNS.Upstream {
		if u == server {
			c.DNS.Upstream = append(c.DNS.Upstream[:i], c.DNS.Upstream[i+1:]...)
			return true
		}
	}
	return false
}

// MergeJSON aplica apenas os campos presentes em in ao Data atual.
// Campos ausentes são preservados, permitindo atualizações parciais pela API.
func (c *Config) MergeJSON(in []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(in, &raw); err != nil {
		return err
	}
	base := c.snapshotLocked()
	for key, val := range raw {
		var err error
		switch key {
		case "node":
			err = json.Unmarshal(val, &base.Node)
		case "api":
			err = json.Unmarshal(val, &base.API)
		case "web":
			err = json.Unmarshal(val, &base.Web)
		case "dns":
			err = json.Unmarshal(val, &base.DNS)
		case "yggdrasil":
			err = json.Unmarshal(val, &base.Yggdrasil)
		case "couchdb":
			err = json.Unmarshal(val, &base.CouchDB)
		case "sites":
			err = json.Unmarshal(val, &base.Sites)
		case "scraper":
			err = json.Unmarshal(val, &base.Scraper)
		case "p2p":
			err = json.Unmarshal(val, &base.P2P)
		case "registry":
			var patch Registry
			if err = json.Unmarshal(val, &patch); err == nil {
				if patch.Enabled != nil {
					base.Registry.Enabled = patch.Enabled
				}
				if patch.Engine != "" {
					base.Registry.Engine = patch.Engine
				}
				if patch.Path != "" {
					base.Registry.Path = patch.Path
				}
				if patch.GossipTopic != "" {
					base.Registry.GossipTopic = patch.GossipTopic
				}
				if patch.ListenPort != 0 {
					base.Registry.ListenPort = patch.ListenPort
				}
				if patch.BootstrapPeers != nil {
					base.Registry.BootstrapPeers = patch.BootstrapPeers
				}
				if patch.StaticPeers != nil {
					base.Registry.StaticPeers = patch.StaticPeers
				}
			}
		case "update":
			err = json.Unmarshal(val, &base.Update)
		case "cache":
			err = json.Unmarshal(val, &base.Cache)
		case "bootstrap":
			err = json.Unmarshal(val, &base.Bootstrap)
		}
		if err != nil {
			return fmt.Errorf("campo %s: %w", key, err)
		}
	}
	c.Data = base
	return nil
}
