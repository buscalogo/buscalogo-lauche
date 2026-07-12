package scraper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"buscalogo-agent/internal/couchdb"
)

type Store struct {
	cdb *couchdb.Service

	hostMu     sync.Mutex
	hostCounts map[string]int
	hostsWarm  bool

	signerMu sync.Mutex
	signer   ScrapeSigner
}

// ScrapeSigner carimba e assina resultados (conta local ed25519).
type ScrapeSigner interface {
	SignScrape(docID, rawURL, updatedAt string) (createdBy any, signature, signedAt string, ok bool)
}

func NewStore(cdb *couchdb.Service) *Store {
	return &Store{cdb: cdb, hostCounts: map[string]int{}}
}

func (st *Store) SetSigner(signer ScrapeSigner) {
	st.signerMu.Lock()
	st.signer = signer
	st.signerMu.Unlock()
}

func docIDForURL(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "scraping_" + hex.EncodeToString(sum[:16])
}

// dbNameForHost gera nome CouchDB válido: bl_scraping_<host_sanitizado>.
func dbNameForHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return hostDBPrefix + "unknown"
	}
	var b strings.Builder
	b.WriteString(hostDBPrefix)
	lastUnderscore := false
	for _, r := range host {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	name := strings.TrimRight(b.String(), "_")
	if !strings.HasPrefix(name, hostDBPrefix) || name == hostDBPrefix {
		name = hostDBPrefix + "unknown"
	}
	// CouchDB limita nomes; deixa folga.
	if len(name) > 200 {
		sum := sha256.Sum256([]byte(host))
		name = hostDBPrefix + hex.EncodeToString(sum[:12])
	}
	return name
}

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}

func slimPageContent(c PageContent) PageContent {
	c.Links = nil
	c.MainText = truncateRunes(c.MainText, maxMainTextBytes)
	if len(c.Paragraphs) > maxParagraphs {
		c.Paragraphs = c.Paragraphs[:maxParagraphs]
	}
	for i := range c.Paragraphs {
		c.Paragraphs[i] = truncateRunes(c.Paragraphs[i], maxParagraphChars)
	}
	if len(c.Headings.H1) > 10 {
		c.Headings.H1 = c.Headings.H1[:10]
	}
	if len(c.Headings.H2) > 20 {
		c.Headings.H2 = c.Headings.H2[:20]
	}
	if len(c.Headings.H3) > 20 {
		c.Headings.H3 = c.Headings.H3[:20]
	}
	if len(c.Images) > maxStoredImages {
		c.Images = c.Images[:maxStoredImages]
	}
	return c
}

func slimMetadata(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	out := map[string]any{}
	if v, ok := meta["favicon"]; ok {
		out["favicon"] = v
	}
	if v, ok := meta["content_type"]; ok {
		out["content_type"] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (st *Store) bumpHost(host string, delta int) {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || delta == 0 {
		return
	}
	st.hostMu.Lock()
	defer st.hostMu.Unlock()
	if st.hostCounts == nil {
		st.hostCounts = map[string]int{}
	}
	st.hostCounts[host] += delta
	if st.hostCounts[host] <= 0 {
		delete(st.hostCounts, host)
	}
}

func (st *Store) ensureHostMeta(db, host string) {
	meta := map[string]any{
		"_id":      siteMetaDocID,
		"doc_type": "site_meta",
		"hostname": strings.ToLower(strings.TrimSpace(host)),
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if existing, rev, err := st.cdb.GetDoc(db, siteMetaDocID); err == nil && len(existing) > 0 {
		meta["_rev"] = rev
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return
	}
	_ = st.cdb.PutDoc(db, siteMetaDocID, body)
}

func (st *Store) readHostMeta(db string) string {
	body, _, err := st.cdb.GetDoc(db, siteMetaDocID)
	if err != nil {
		return ""
	}
	var meta struct {
		Hostname string `json:"hostname"`
	}
	if json.Unmarshal(body, &meta) != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(meta.Hostname))
}

func (st *Store) Save(task *Task, result ScrapeResult, scheduleDays int) error {
	now := time.Now().UTC()
	var nextCheck *string
	if scheduleDays > 0 {
		t := now.Add(time.Duration(scheduleDays) * 24 * time.Hour).Format(time.RFC3339)
		nextCheck = &t
	}
	searchText := extractSearchableText(result)
	searchText = truncateRunes(searchText, maxStoredTextBytes)
	terms := extractTerms(searchText, 3)
	if len(terms) > maxStoredTerms {
		terms = terms[:maxStoredTerms]
	}

	u, _ := url.Parse(result.URL)
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	db := dbNameForHost(host)
	if err := st.cdb.EnsureDB(db); err != nil {
		return fmt.Errorf("criar db %s: %w", db, err)
	}
	st.ensureHostMeta(db, host)

	content := slimPageContent(result.Content)
	doc := StoredDoc{
		ID:              docIDForURL(result.URL),
		TaskID:          task.ID,
		URL:             result.URL,
		Title:           result.Content.Title,
		Description:     truncateRunes(result.Content.Description, 1000),
		Text:            searchText,
		Terms:           terms,
		Hostname:        host,
		Content:         content,
		Analysis:        result.Analysis,
		DiscoveredLinks: result.DiscoveredLinks,
		InternalLinks:   nil, // não duplicar discovered_links
		Metadata:        slimMetadata(result.Metadata),
		CreatedAt:       now.Format(time.RFC3339),
		UpdatedAt:       now.Format(time.RFC3339),
		NextCheckAt:     nextCheck,
		ScheduleDays:    scheduleDays,
		DocType:         "enhanced_scraping_result",
	}
	st.signerMu.Lock()
	signer := st.signer
	st.signerMu.Unlock()
	if signer != nil {
		if cb, sig, sat, ok := signer.SignScrape(doc.ID, doc.URL, doc.UpdatedAt); ok {
			doc.CreatedBy = cb
			doc.Signature = sig
			doc.SignedAt = sat
		}
	}
	isNew := true
	if existing, rev, err := st.cdb.GetDoc(db, doc.ID); err == nil && len(existing) > 0 {
		isNew = false
		var prev StoredDoc
		if json.Unmarshal(existing, &prev) == nil && prev.CreatedAt != "" {
			doc.CreatedAt = prev.CreatedAt
		}
		doc.Rev = rev
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := st.cdb.PutDoc(db, doc.ID, body); err != nil {
		return err
	}
	if isNew {
		st.bumpHost(host, 1)
	}
	return nil
}

// listResultMeta só decodifica campos leves.
type listResultMeta struct {
	ID        string `json:"_id"`
	Rev       string `json:"_rev"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Hostname  string `json:"hostname"`
	UpdatedAt string `json:"updated_at"`
	CreatedAt string `json:"created_at"`
	DocType   string `json:"doc_type"`
}

func (st *Store) hostDatabases() ([]string, error) {
	dbs, err := st.cdb.ListDatabases()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, name := range dbs {
		if strings.HasPrefix(name, hostDBPrefix) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (st *Store) ListResults(limit int) ([]StoredDoc, error) {
	return st.listResultsAcross(limit, false)
}

// ListResultsFull carrega documentos completos (busca P2P / relevância).
func (st *Store) ListResultsFull(limit int) ([]StoredDoc, error) {
	return st.listResultsAcross(limit, true)
}

func (st *Store) listResultsAcross(limit int, full bool) ([]StoredDoc, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	dbs, err := st.hostDatabases()
	if err != nil {
		dbs = nil
	}
	// Inclui legado no fim para amostra, se ainda existir.
	dbs = append(append([]string{}, dbs...), scrapingDBLegacy)

	out := make([]StoredDoc, 0, limit)
	perDB := limit
	if len(dbs) > 1 {
		perDB = (limit / len(dbs)) + 1
		if perDB < 5 {
			perDB = 5
		}
		if perDB > 40 {
			perDB = 40
		}
	}
	for _, db := range dbs {
		if len(out) >= limit {
			break
		}
		need := limit - len(out)
		if need > perDB {
			need = perDB
		}
		part, listErr := st.listResultsInDB(db, need, 0, full)
		if listErr != nil {
			continue
		}
		out = append(out, part...)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (st *Store) listResultsInDB(db string, limit, skip int, full bool) ([]StoredDoc, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if skip < 0 {
		skip = 0
	}
	rows, err := st.cdb.ListDocsPaged(db, "scraping_", "scraping_\ufff0", limit, skip)
	if err != nil {
		return nil, err
	}
	out := make([]StoredDoc, 0, len(rows))
	for _, row := range rows {
		if full {
			var doc StoredDoc
			if json.Unmarshal(row.Doc, &doc) != nil {
				continue
			}
			if doc.DocType != "enhanced_scraping_result" && doc.DocType != "scraping_result" {
				continue
			}
			doc.ID = row.ID
			doc.Rev = row.Rev
			out = append(out, doc)
			continue
		}
		var meta listResultMeta
		if json.Unmarshal(row.Doc, &meta) != nil {
			continue
		}
		if meta.DocType != "enhanced_scraping_result" && meta.DocType != "scraping_result" {
			continue
		}
		out = append(out, StoredDoc{
			ID:        row.ID,
			Rev:       row.Rev,
			URL:       meta.URL,
			Title:     meta.Title,
			Hostname:  meta.Hostname,
			UpdatedAt: meta.UpdatedAt,
			CreatedAt: meta.CreatedAt,
			DocType:   meta.DocType,
		})
	}
	return out, nil
}

func (st *Store) findDocLocation(docID string) (db string, body []byte, rev string, err error) {
	docID = strings.TrimSpace(docID)
	if docID == "" {
		return "", nil, "", fmt.Errorf("docId vazio")
	}
	dbs, listErr := st.hostDatabases()
	if listErr != nil {
		dbs = nil
	}
	candidates := append(dbs, scrapingDBLegacy)
	for _, name := range candidates {
		b, r, getErr := st.cdb.GetDoc(name, docID)
		if getErr != nil {
			continue
		}
		return name, b, r, nil
	}
	return "", nil, "", fmt.Errorf("not found")
}

func (st *Store) Delete(docID string) error {
	db, body, rev, err := st.findDocLocation(docID)
	if err != nil {
		return err
	}
	var doc StoredDoc
	_ = json.Unmarshal(body, &doc)
	if err := st.cdb.DeleteDoc(db, docID, rev); err != nil {
		return err
	}
	host := doc.Hostname
	if host == "" && doc.URL != "" {
		if u, err := url.Parse(doc.URL); err == nil {
			host = u.Hostname()
		}
	}
	st.bumpHost(host, -1)
	return nil
}

// SiteSummary agrega páginas indexadas por hostname.
type SiteSummary struct {
	Hostname      string `json:"hostname"`
	Count         int    `json:"count"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	Database      string `json:"database,omitempty"`
	Legacy        bool   `json:"legacy,omitempty"`
	LastUpdatedAt string `json:"last_updated_at,omitempty"`
	SampleURL     string `json:"sample_url,omitempty"`
	SampleTitle   string `json:"sample_title,omitempty"`
}

func hostnameMatchesSite(docHost, siteHost string) bool {
	docHost = strings.ToLower(strings.TrimSpace(docHost))
	siteHost = strings.ToLower(strings.TrimSpace(siteHost))
	if docHost == "" || siteHost == "" {
		return false
	}
	if docHost == siteHost {
		return true
	}
	return baseDomain(docHost) == baseDomain(siteHost)
}

// WarmHostCounts preenche contadores a partir dos DBs por host (leve).
func (st *Store) WarmHostCounts() {
	st.hostMu.Lock()
	if st.hostsWarm {
		st.hostMu.Unlock()
		return
	}
	st.hostMu.Unlock()

	sites, err := st.ListSites()
	if err != nil {
		return
	}
	st.hostMu.Lock()
	defer st.hostMu.Unlock()
	if st.hostsWarm {
		return
	}
	st.hostCounts = map[string]int{}
	for _, s := range sites {
		if s.Legacy || s.Hostname == "" {
			continue
		}
		st.hostCounts[s.Hostname] = s.Count
	}
	st.hostsWarm = true
}

// ListSites lista DBs bl_scraping_* + resumo do legado.
func (st *Store) ListSites() ([]SiteSummary, error) {
	dbs, err := st.hostDatabases()
	if err != nil {
		return nil, err
	}
	out := make([]SiteSummary, 0, len(dbs)+1)
	for _, db := range dbs {
		info, infoErr := st.cdb.DbInfo(db)
		host := st.readHostMeta(db)
		if host == "" {
			host = strings.TrimPrefix(db, hostDBPrefix)
			host = strings.ReplaceAll(host, "_", ".")
		}
		count := int(info.DocCount)
		if count > 0 {
			// meta doc não é página
			count--
		}
		if count < 0 {
			count = 0
		}
		if infoErr != nil {
			count = 0
		}
		sum := SiteSummary{
			Hostname:  host,
			Count:     count,
			SizeBytes: info.FileSize,
			Database:  db,
		}
		// Hostname preferencial do meta (sem include_docs — docs legados são enormes).
		out = append(out, sum)
	}

	if legacy, legErr := st.cdb.DbInfo(scrapingDBLegacy); legErr == nil && legacy.DocCount > 0 {
		out = append(out, SiteSummary{
			Hostname:  "(legado monólito)",
			Count:     int(legacy.DocCount),
			SizeBytes: legacy.FileSize,
			Database:  scrapingDBLegacy,
			Legacy:    true,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Legacy != out[j].Legacy {
			return !out[i].Legacy && out[j].Legacy
		}
		if out[i].SizeBytes == out[j].SizeBytes {
			if out[i].Count == out[j].Count {
				return out[i].Hostname < out[j].Hostname
			}
			return out[i].Count > out[j].Count
		}
		return out[i].SizeBytes > out[j].SizeBytes
	})
	return out, nil
}

// DeleteByHostname remove o DB do host (instantâneo). Legado: varredura limitada.
func (st *Store) DeleteByHostname(hostname string) (deleted int, err error) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return 0, fmt.Errorf("hostname vazio")
	}
	if hostname == scrapingDBLegacy || hostname == "__legacy__" || strings.HasPrefix(hostname, "(legado") {
		return 0, fmt.Errorf("use a ação de apagar banco legado")
	}

	db := dbNameForHost(hostname)
	info, _ := st.cdb.DbInfo(db)
	before := int(info.DocCount)

	// Também tenta DBs cujo meta hostname casa (sanitização ambígua).
	dbs, _ := st.hostDatabases()
	targets := map[string]bool{db: true}
	for _, name := range dbs {
		metaHost := st.readHostMeta(name)
		if metaHost != "" && hostnameMatchesSite(metaHost, hostname) {
			targets[name] = true
		}
	}

	for name := range targets {
		inf, _ := st.cdb.DbInfo(name)
		n := int(inf.DocCount)
		if err := st.cdb.DeleteDB(name); err != nil {
			return deleted, err
		}
		deleted += n
	}
	if deleted == 0 && before > 0 {
		deleted = before
	}

	// Limpa eventuais restos no monólito legado (amostra limitada).
	legacyDeleted, _ := st.deleteHostnameFromLegacy(hostname)
	deleted += legacyDeleted

	st.hostMu.Lock()
	for host := range st.hostCounts {
		if hostnameMatchesSite(host, hostname) {
			delete(st.hostCounts, host)
		}
	}
	st.hostMu.Unlock()
	return deleted, nil
}

func (st *Store) deleteHostnameFromLegacy(hostname string) (deleted int, err error) {
	const pageSize = 50
	const maxPages = 20
	skip := 0
	for page := 0; page < maxPages; page++ {
		docs, listErr := st.listResultsInDB(scrapingDBLegacy, pageSize, skip, false)
		if listErr != nil {
			return deleted, listErr
		}
		if len(docs) == 0 {
			break
		}
		batch := 0
		for _, doc := range docs {
			if !hostnameMatchesSite(doc.Hostname, hostname) {
				continue
			}
			body, rev, getErr := st.cdb.GetDoc(scrapingDBLegacy, doc.ID)
			_ = body
			if getErr != nil {
				continue
			}
			if delErr := st.cdb.DeleteDoc(scrapingDBLegacy, doc.ID, rev); delErr != nil {
				continue
			}
			deleted++
			batch++
		}
		if batch == 0 {
			skip += len(docs)
			continue
		}
	}
	return deleted, nil
}

// DeleteLegacyDB remove o monólito buscalogo_scraping inteiro.
func (st *Store) DeleteLegacyDB() (couchdb.DbInfo, error) {
	info, _ := st.cdb.DbInfo(scrapingDBLegacy)
	if err := st.cdb.DeleteDB(scrapingDBLegacy); err != nil {
		return info, err
	}
	// Recria vazio para bootstrap/compat (PouchDB / defaults).
	_ = st.cdb.EnsureDB(scrapingDBLegacy)
	st.hostMu.Lock()
	st.hostsWarm = false
	st.hostCounts = map[string]int{}
	st.hostMu.Unlock()
	return info, nil
}

// StorageStats resume tamanho do índice (só DbInfo em cache — sem include_docs).
func (st *Store) StorageStats() map[string]any {
	dbs, err := st.hostDatabases()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var hostDocs int64
	var hostBytes int64
	for _, db := range dbs {
		info, infoErr := st.cdb.DbInfo(db)
		if infoErr != nil {
			continue
		}
		n := info.DocCount
		if n > 0 {
			n-- // meta
		}
		hostDocs += n
		hostBytes += info.FileSize
	}
	var legacyDocs, legacyBytes int64
	if legacy, legErr := st.cdb.DbInfo(scrapingDBLegacy); legErr == nil {
		legacyDocs = legacy.DocCount
		legacyBytes = legacy.FileSize
	}
	return map[string]any{
		"host_databases": len(dbs),
		"doc_count":      hostDocs + legacyDocs,
		"file_size":      hostBytes + legacyBytes,
		"legacy_docs":    legacyDocs,
		"legacy_bytes":   legacyBytes,
		"legacy_db":      scrapingDBLegacy,
		"host_prefix":    hostDBPrefix,
	}
}

// LookupResult é o status de indexação de uma URL.
type LookupResult struct {
	Indexed   bool   `json:"indexed"`
	DocID     string `json:"docId"`
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func (st *Store) Lookup(rawURL string) (LookupResult, error) {
	rawURL = strings.TrimSpace(rawURL)
	docID := docIDForURL(rawURL)
	out := LookupResult{Indexed: false, DocID: docID, URL: rawURL}
	if rawURL == "" {
		return out, fmt.Errorf("url vazia")
	}
	host := ""
	if u, err := url.Parse(rawURL); err == nil {
		host = u.Hostname()
	}
	tryDBs := []string{}
	if host != "" {
		tryDBs = append(tryDBs, dbNameForHost(host))
	}
	tryDBs = append(tryDBs, scrapingDBLegacy)

	var body []byte
	var err error
	for _, db := range tryDBs {
		body, _, err = st.cdb.GetDoc(db, docID)
		if err == nil {
			break
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return out, nil
		}
		return out, err
	}
	var doc StoredDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return out, err
	}
	out.Indexed = true
	out.Title = doc.Title
	out.UpdatedAt = doc.UpdatedAt
	if doc.URL != "" {
		out.URL = doc.URL
	}
	return out, nil
}
