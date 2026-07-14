package scraper

import "fmt"

// ErrStoreUnavailable — índice de scrape não disponível neste nó.
var ErrStoreUnavailable = fmt.Errorf("índice de scrape indisponível")

// LegacyPurgeInfo resume a purga do monólito legado (Couch) ou equivalente.
type LegacyPurgeInfo struct {
	DocCount int64 `json:"doc_count"`
	FileSize int64 `json:"file_size"`
}

// ScrapeStore é o índice local de páginas (CouchDB no Linux, SQLite no Windows).
type ScrapeStore interface {
	SetSigner(signer ScrapeSigner)
	Save(task *Task, result ScrapeResult, scheduleDays int) error
	ListResults(limit int) ([]StoredDoc, error)
	ListResultsFull(limit int) ([]StoredDoc, error)
	Search(query, queryID, peerID string, limit int) ([]SearchHit, error)
	Lookup(rawURL string) (LookupResult, error)
	Delete(docID string) error
	ListSites() ([]SiteSummary, error)
	DeleteByHostname(hostname string) (deleted int, err error)
	DeleteLegacyDB() (LegacyPurgeInfo, error)
	StorageStats() map[string]any
	WarmHostCounts()
}
