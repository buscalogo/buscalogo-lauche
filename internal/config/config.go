package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	Listen          string `yaml:"listen" json:"listen"`
	Port            int    `yaml:"port" json:"port"`
	ExternalListen  bool   `yaml:"external_listen" json:"external_listen"` // escutar em todas as interfaces (inclui Yggdrasil)
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
	Enabled bool `yaml:"enabled" json:"enabled"`
}

type Cache struct {
	Size int `yaml:"size" json:"size"`
}

func Default() *Config {
	return &Config{Data: Data{
		Node: Node{Name: "BuscaLogo Node"},
		API:  API{Listen: "127.0.0.1:9970"},
		Web:  Web{Listen: "127.0.0.1", Port: 80, ExternalListen: true},
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
			Enabled:   true,
			Mode:      "own",
			Listen:    "127.0.0.1",
			Port:      5984,
			Databases: DefaultCouchDBDatabases,
		},
		Sites: []Site{
			{Host: "buscalogo.bl", Type: "static", Root: defaultBuscaLogoDist(), Enabled: true},
		},
		Scraper: Scraper{Enabled: false},
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
	if c.DNS.Mode == "" {
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
