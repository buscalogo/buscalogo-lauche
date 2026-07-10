package scraper

import (
	"fmt"
	"sync"
	"time"

	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/couchdb"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/process"
)

// Service é o motor de scraping nativo (CouchDB / buscalogo_scraping).
type Service struct {
	cfg    *config.Config
	buf    *logx.Buffer
	cdb    *couchdb.Service
	store  *Store
	engine *Engine
	mu     sync.Mutex
}

func New(cfg *config.Config, cdb *couchdb.Service, buf *logx.Buffer) *Service {
	s := &Service{cfg: cfg, buf: buf, cdb: cdb}
	if cdb != nil {
		s.store = NewStore(cdb)
	}
	return s
}

func (s *Service) runtimeConfig() RuntimeConfig {
	sc := s.cfg.Scraper
	rc := RuntimeConfig{
		MaxConcurrent:        sc.MaxConcurrent,
		MaxDepth:             sc.MaxDepth,
		MaxRetries:           sc.MaxRetries,
		MaxLinksPerPage:      sc.MaxLinksPerPage,
		DiscoverInternalOnly: sc.DiscoverInternalOnly,
		DefaultScheduleDays:  sc.DefaultScheduleDays,
		BlockedDomains:       append([]string(nil), sc.BlockedDomains...),
		AllowedDomains:       append([]string(nil), sc.AllowedDomains...),
	}
	if rc.MaxConcurrent <= 0 {
		rc.MaxConcurrent = 3
	}
	if rc.MaxDepth <= 0 {
		rc.MaxDepth = 3
	}
	if rc.MaxRetries <= 0 {
		rc.MaxRetries = 3
	}
	if rc.RequestDelay <= 0 {
		if sc.RequestDelayMs > 0 {
			rc.RequestDelay = time.Duration(sc.RequestDelayMs) * time.Millisecond
		} else {
			rc.RequestDelay = 2 * time.Second
		}
	}
	if rc.MaxLinksPerPage <= 0 {
		rc.MaxLinksPerPage = 100
	}
	if rc.DefaultScheduleDays <= 0 {
		rc.DefaultScheduleDays = 7
	}
	return rc
}

func (s *Service) ensureEngine() *Engine {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.engine == nil {
		s.engine = NewEngine(s.runtimeConfig(), s.store, s.buf)
	} else {
		s.engine.SetConfig(s.runtimeConfig())
	}
	return s.engine
}

func (s *Service) Start() error {
	eng := s.ensureEngine()
	eng.Start()
	s.buf.Infof("scraper", "motor de scraping iniciado (CouchDB: %s)", scrapingDB)
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	eng := s.engine
	s.mu.Unlock()
	if eng == nil {
		return nil
	}
	eng.Stop()
	s.buf.Infof("scraper", "motor de scraping parado")
	return nil
}

func (s *Service) Restart() error {
	if err := s.Stop(); err != nil {
		return err
	}
	return s.Start()
}

func (s *Service) Status() process.Status {
	st := process.Status{Name: "Scraper", Binary: "native"}
	s.mu.Lock()
	eng := s.engine
	s.mu.Unlock()
	if eng != nil && eng.Running() {
		st.State = process.StateRunning
		st.Uptime = eng.Stats().UptimeMs / 1000
	} else {
		st.State = process.StateStopped
	}
	return st
}

func (s *Service) Engine() *Engine {
	return s.ensureEngine()
}

func (s *Service) Info() map[string]any {
	s.mu.Lock()
	eng := s.engine
	s.mu.Unlock()
	info := map[string]any{
		"backend":  "couchdb",
		"database": scrapingDB,
		"running":  false,
	}
	if s.cdb != nil {
		info["couchdb_url"] = s.cdb.BaseURL()
	}
	if eng != nil {
		info["running"] = eng.Running()
		info["stats"] = eng.Stats()
		info["config"] = s.ConfigMap()
	}
	return info
}

func (s *Service) ConfigMap() map[string]any {
	rc := s.runtimeConfig()
	return map[string]any{
		"max_concurrent":         rc.MaxConcurrent,
		"max_depth":              rc.MaxDepth,
		"max_retries":            rc.MaxRetries,
		"request_delay_ms":       int(rc.RequestDelay / time.Millisecond),
		"max_links_per_page":     rc.MaxLinksPerPage,
		"discover_internal_only": rc.DiscoverInternalOnly,
		"default_schedule_days":  rc.DefaultScheduleDays,
		"blocked_domains":        rc.BlockedDomains,
		"allowed_domains":        rc.AllowedDomains,
	}
}

func (s *Service) ApplyConfig(updates map[string]any) error {
	sc := &s.cfg.Scraper
	if v, ok := updates["max_concurrent"].(float64); ok {
		sc.MaxConcurrent = int(v)
	}
	if v, ok := updates["max_depth"].(float64); ok {
		sc.MaxDepth = int(v)
	}
	if v, ok := updates["max_retries"].(float64); ok {
		sc.MaxRetries = int(v)
	}
	if v, ok := updates["request_delay_ms"].(float64); ok {
		sc.RequestDelayMs = int(v)
	}
	if v, ok := updates["max_links_per_page"].(float64); ok {
		sc.MaxLinksPerPage = int(v)
	}
	if v, ok := updates["discover_internal_only"].(bool); ok {
		sc.DiscoverInternalOnly = v
	}
	if v, ok := updates["default_schedule_days"].(float64); ok {
		sc.DefaultScheduleDays = int(v)
	}
	if v, ok := updates["blocked_domains"].([]any); ok {
		sc.BlockedDomains = stringsFromAny(v)
	}
	if v, ok := updates["allowed_domains"].([]any); ok {
		sc.AllowedDomains = stringsFromAny(v)
	}
	if err := s.cfg.Save(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.engine != nil {
		s.engine.SetConfig(s.runtimeConfig())
	}
	s.mu.Unlock()
	return nil
}

func stringsFromAny(list []any) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (s *Service) AddTask(url, priority string, depth, maxDepth, scheduleDays int, discoveredFrom, taskType string) (string, error) {
	eng := s.ensureEngine()
	if !eng.Running() {
		s.buf.Infof("scraper", "scraper parado — iniciando automaticamente para enfileirar tarefa")
		if err := s.Start(); err != nil {
			return "", fmt.Errorf("iniciar scraper: %w", err)
		}
		eng = s.ensureEngine()
	}
	p := Priority(priority)
	if p == "" {
		p = PriorityNormal
	}
	return eng.AddToQueue(url, p, depth, maxDepth, scheduleDays, discoveredFrom, taskType)
}

func (s *Service) Lookup(rawURL string) (LookupResult, error) {
	if s.store == nil {
		return LookupResult{}, fmt.Errorf("CouchDB indisponível")
	}
	return s.store.Lookup(rawURL)
}

func (s *Service) ListResults(limit int) ([]StoredDoc, error) {
	if s.store == nil {
		return nil, fmt.Errorf("CouchDB indisponível")
	}
	return s.store.ListResults(limit)
}

func (s *Service) DeleteResult(docID string) error {
	if s.store == nil {
		return fmt.Errorf("CouchDB indisponível")
	}
	return s.store.Delete(docID)
}
