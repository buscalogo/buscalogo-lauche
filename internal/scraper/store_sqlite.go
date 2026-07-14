package scraper

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore índice de scrape em ficheiro local (Windows / sem CouchDB).
type SQLiteStore struct {
	db   *sql.DB
	path string

	signerMu sync.Mutex
	signer   ScrapeSigner
}

// OpenSQLiteStore abre ou cria o índice em path (ex.: data/scrape/index.sqlite).
func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrateSQLite(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db, path: path}, nil
}

func migrateSQLite(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS pages (
  doc_id          TEXT PRIMARY KEY,
  url             TEXT NOT NULL UNIQUE,
  task_id         TEXT NOT NULL DEFAULT '',
  title           TEXT NOT NULL DEFAULT '',
  description     TEXT NOT NULL DEFAULT '',
  text            TEXT NOT NULL DEFAULT '',
  terms_json      TEXT NOT NULL DEFAULT '[]',
  hostname        TEXT NOT NULL COLLATE NOCASE,
  content_json    TEXT NOT NULL DEFAULT '{}',
  analysis_json   TEXT NOT NULL DEFAULT '{}',
  discovered_json TEXT NOT NULL DEFAULT '[]',
  metadata_json   TEXT NOT NULL DEFAULT '{}',
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  next_check_at   TEXT,
  schedule_days   INTEGER NOT NULL DEFAULT 0,
  doc_type        TEXT NOT NULL DEFAULT 'enhanced_scraping_result',
  created_by_json TEXT,
  signature       TEXT,
  signed_at       TEXT
);
CREATE INDEX IF NOT EXISTS idx_pages_hostname ON pages(hostname);
CREATE INDEX IF NOT EXISTS idx_pages_updated ON pages(updated_at DESC);
`)
	return err
}

func (st *SQLiteStore) Close() error {
	if st == nil || st.db == nil {
		return nil
	}
	return st.db.Close()
}

func (st *SQLiteStore) SetSigner(signer ScrapeSigner) {
	st.signerMu.Lock()
	st.signer = signer
	st.signerMu.Unlock()
}

func (st *SQLiteStore) WarmHostCounts() {}

func (st *SQLiteStore) buildDoc(task *Task, result ScrapeResult, scheduleDays int) StoredDoc {
	now := time.Now().UTC()
	var nextCheck *string
	if scheduleDays > 0 {
		t := now.Add(time.Duration(scheduleDays) * 24 * time.Hour).Format(time.RFC3339)
		nextCheck = &t
	}
	searchText := truncateRunes(extractSearchableText(result), maxStoredTextBytes)
	terms := extractTerms(searchText, 3)
	if len(terms) > maxStoredTerms {
		terms = terms[:maxStoredTerms]
	}
	host := ""
	if u, err := url.Parse(result.URL); err == nil && u != nil {
		host = u.Hostname()
	}
	doc := StoredDoc{
		ID:              docIDForURL(result.URL),
		TaskID:          task.ID,
		URL:             result.URL,
		Title:           result.Content.Title,
		Description:     truncateRunes(result.Content.Description, 1000),
		Text:            searchText,
		Terms:           terms,
		Hostname:        host,
		Content:         slimPageContent(result.Content),
		Analysis:        result.Analysis,
		DiscoveredLinks: result.DiscoveredLinks,
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
	return doc
}

func (st *SQLiteStore) Save(task *Task, result ScrapeResult, scheduleDays int) error {
	doc := st.buildDoc(task, result, scheduleDays)
	var prevCreated string
	_ = st.db.QueryRow(`SELECT created_at FROM pages WHERE doc_id = ?`, doc.ID).Scan(&prevCreated)
	if prevCreated != "" {
		doc.CreatedAt = prevCreated
	}

	termsJSON, _ := json.Marshal(doc.Terms)
	contentJSON, _ := json.Marshal(doc.Content)
	analysisJSON, _ := json.Marshal(doc.Analysis)
	discoveredJSON, _ := json.Marshal(doc.DiscoveredLinks)
	metaJSON, _ := json.Marshal(doc.Metadata)
	var createdByJSON []byte
	if doc.CreatedBy != nil {
		createdByJSON, _ = json.Marshal(doc.CreatedBy)
	}
	var next sql.NullString
	if doc.NextCheckAt != nil {
		next = sql.NullString{String: *doc.NextCheckAt, Valid: true}
	}

	_, err := st.db.Exec(`
INSERT INTO pages (
  doc_id, url, task_id, title, description, text, terms_json, hostname,
  content_json, analysis_json, discovered_json, metadata_json,
  created_at, updated_at, next_check_at, schedule_days, doc_type,
  created_by_json, signature, signed_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(doc_id) DO UPDATE SET
  url=excluded.url, task_id=excluded.task_id, title=excluded.title,
  description=excluded.description, text=excluded.text, terms_json=excluded.terms_json,
  hostname=excluded.hostname, content_json=excluded.content_json,
  analysis_json=excluded.analysis_json, discovered_json=excluded.discovered_json,
  metadata_json=excluded.metadata_json, updated_at=excluded.updated_at,
  next_check_at=excluded.next_check_at, schedule_days=excluded.schedule_days,
  doc_type=excluded.doc_type, created_by_json=excluded.created_by_json,
  signature=excluded.signature, signed_at=excluded.signed_at
`,
		doc.ID, doc.URL, doc.TaskID, doc.Title, doc.Description, doc.Text, string(termsJSON), doc.Hostname,
		string(contentJSON), string(analysisJSON), string(discoveredJSON), string(metaJSON),
		doc.CreatedAt, doc.UpdatedAt, next, doc.ScheduleDays, doc.DocType,
		nullableJSON(createdByJSON), doc.Signature, doc.SignedAt,
	)
	return err
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func (st *SQLiteStore) scanPage(rows interface {
	Scan(dest ...any) error
}) (StoredDoc, error) {
	var doc StoredDoc
	var termsJSON, contentJSON, analysisJSON, discoveredJSON, metaJSON string
	var next sql.NullString
	var createdBy sql.NullString
	var sig, signedAt sql.NullString
	err := rows.Scan(
		&doc.ID, &doc.URL, &doc.TaskID, &doc.Title, &doc.Description, &doc.Text, &termsJSON, &doc.Hostname,
		&contentJSON, &analysisJSON, &discoveredJSON, &metaJSON,
		&doc.CreatedAt, &doc.UpdatedAt, &next, &doc.ScheduleDays, &doc.DocType,
		&createdBy, &sig, &signedAt,
	)
	if err != nil {
		return doc, err
	}
	_ = json.Unmarshal([]byte(termsJSON), &doc.Terms)
	_ = json.Unmarshal([]byte(contentJSON), &doc.Content)
	_ = json.Unmarshal([]byte(analysisJSON), &doc.Analysis)
	_ = json.Unmarshal([]byte(discoveredJSON), &doc.DiscoveredLinks)
	_ = json.Unmarshal([]byte(metaJSON), &doc.Metadata)
	if next.Valid {
		s := next.String
		doc.NextCheckAt = &s
	}
	if createdBy.Valid && createdBy.String != "" {
		var cb any
		if json.Unmarshal([]byte(createdBy.String), &cb) == nil {
			doc.CreatedBy = cb
		}
	}
	if sig.Valid {
		doc.Signature = sig.String
	}
	if signedAt.Valid {
		doc.SignedAt = signedAt.String
	}
	return doc, nil
}

const pageSelectCols = `doc_id, url, task_id, title, description, text, terms_json, hostname,
  content_json, analysis_json, discovered_json, metadata_json,
  created_at, updated_at, next_check_at, schedule_days, doc_type,
  created_by_json, signature, signed_at`

func (st *SQLiteStore) ListResults(limit int) ([]StoredDoc, error) {
	return st.list(limit, false)
}

func (st *SQLiteStore) ListResultsFull(limit int) ([]StoredDoc, error) {
	return st.list(limit, true)
}

func (st *SQLiteStore) list(limit int, full bool) ([]StoredDoc, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := st.db.Query(`SELECT `+pageSelectCols+` FROM pages ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StoredDoc, 0, limit)
	for rows.Next() {
		doc, err := st.scanPage(rows)
		if err != nil {
			return nil, err
		}
		if !full {
			doc.Text = ""
			doc.Terms = nil
			doc.Content = PageContent{}
			doc.Analysis = Analysis{}
			doc.DiscoveredLinks = nil
			doc.Metadata = nil
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func (st *SQLiteStore) Search(query, queryID, peerID string, limit int) ([]SearchHit, error) {
	docs, err := st.ListResultsFull(100)
	if err != nil {
		return nil, err
	}
	return rankSearchDocs(docs, query, queryID, peerID, limit), nil
}

func (st *SQLiteStore) Lookup(rawURL string) (LookupResult, error) {
	rawURL = strings.TrimSpace(rawURL)
	docID := docIDForURL(rawURL)
	out := LookupResult{Indexed: false, DocID: docID, URL: rawURL}
	if rawURL == "" {
		return out, fmt.Errorf("url vazia")
	}
	row := st.db.QueryRow(`SELECT title, updated_at, url FROM pages WHERE doc_id = ?`, docID)
	var title, updated, u string
	if err := row.Scan(&title, &updated, &u); err != nil {
		if err == sql.ErrNoRows {
			return out, nil
		}
		return out, err
	}
	out.Indexed = true
	out.Title = title
	out.UpdatedAt = updated
	if u != "" {
		out.URL = u
	}
	return out, nil
}

func (st *SQLiteStore) Delete(docID string) error {
	res, err := st.db.Exec(`DELETE FROM pages WHERE doc_id = ?`, docID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("documento não encontrado")
	}
	return nil
}

func (st *SQLiteStore) ListSites() ([]SiteSummary, error) {
	rows, err := st.db.Query(`
SELECT hostname, COUNT(*) AS n,
       COALESCE(SUM(LENGTH(content_json)+LENGTH(text)+LENGTH(description)), 0) AS sz,
       MAX(updated_at) AS lu
FROM pages GROUP BY hostname ORDER BY n DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SiteSummary, 0)
	for rows.Next() {
		var s SiteSummary
		var count int
		var sz int64
		var lu string
		if err := rows.Scan(&s.Hostname, &count, &sz, &lu); err != nil {
			return nil, err
		}
		s.Count = count
		s.SizeBytes = sz
		s.LastUpdatedAt = lu
		s.Database = "sqlite"
		out = append(out, s)
	}
	return out, rows.Err()
}

func (st *SQLiteStore) DeleteByHostname(hostname string) (int, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return 0, fmt.Errorf("hostname vazio")
	}
	// Variantes: host exacto ou subdomínio.
	res, err := st.db.Exec(`
DELETE FROM pages WHERE lower(hostname) = ? OR lower(hostname) LIKE ?`,
		hostname, "%."+hostname)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (st *SQLiteStore) DeleteLegacyDB() (LegacyPurgeInfo, error) {
	// SQLite não tem monólito separado — no-op compatível com a API.
	return LegacyPurgeInfo{}, nil
}

func (st *SQLiteStore) StorageStats() map[string]any {
	var docs, bytes int64
	_ = st.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(LENGTH(content_json)+LENGTH(text)+LENGTH(description)),0) FROM pages`).Scan(&docs, &bytes)
	var hosts int64
	_ = st.db.QueryRow(`SELECT COUNT(DISTINCT hostname) FROM pages`).Scan(&hosts)
	path := st.path
	if fi, err := os.Stat(path); err == nil {
		bytes = fi.Size()
	}
	return map[string]any{
		"backend":        "sqlite",
		"path":           path,
		"host_databases": hosts,
		"doc_count":      docs,
		"file_size":      bytes,
		"legacy_docs":    int64(0),
		"legacy_bytes":   int64(0),
		"legacy_db":      "",
		"host_prefix":    "sqlite",
	}
}

// Compile-time check.
var _ ScrapeStore = (*SQLiteStore)(nil)
var _ ScrapeStore = (*Store)(nil)
