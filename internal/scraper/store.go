package scraper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"buscalogo-agent/internal/couchdb"
)

type Store struct {
	cdb *couchdb.Service
}

func NewStore(cdb *couchdb.Service) *Store {
	return &Store{cdb: cdb}
}

func docIDForURL(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "scraping_" + hex.EncodeToString(sum[:16])
}

func (st *Store) Save(task *Task, result ScrapeResult, scheduleDays int) error {
	now := time.Now().UTC()
	var nextCheck *string
	if scheduleDays > 0 {
		t := now.Add(time.Duration(scheduleDays) * 24 * time.Hour).Format(time.RFC3339)
		nextCheck = &t
	}
	searchText := extractSearchableText(result)
	internal := make([]ScrapedLink, 0)
	for _, l := range result.DiscoveredLinks {
		if l.IsInternal {
			internal = append(internal, l)
		}
	}
	u, _ := url.Parse(result.URL)
	doc := StoredDoc{
		ID:              docIDForURL(result.URL),
		TaskID:          task.ID,
		URL:             result.URL,
		Title:           result.Content.Title,
		Description:     result.Content.Description,
		Text:            searchText,
		Terms:           extractTerms(searchText, 3),
		Hostname:        u.Hostname(),
		Content:         result.Content,
		Analysis:        result.Analysis,
		DiscoveredLinks: result.DiscoveredLinks,
		InternalLinks:   internal,
		Metadata:        result.Metadata,
		CreatedAt:       now.Format(time.RFC3339),
		UpdatedAt:       now.Format(time.RFC3339),
		NextCheckAt:     nextCheck,
		ScheduleDays:    scheduleDays,
		DocType:         "enhanced_scraping_result",
	}
	if existing, rev, err := st.cdb.GetDoc(scrapingDB, doc.ID); err == nil && len(existing) > 0 {
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
	return st.cdb.PutDoc(scrapingDB, doc.ID, body)
}

func (st *Store) ListResults(limit int) ([]StoredDoc, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := st.cdb.ListDocs(scrapingDB, "scraping_", "scraping_\ufff0", limit)
	if err != nil {
		return nil, err
	}
	out := make([]StoredDoc, 0, len(rows))
	for _, row := range rows {
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
	}
	return out, nil
}

func (st *Store) Delete(docID string) error {
	_, rev, err := st.cdb.GetDoc(scrapingDB, docID)
	if err != nil {
		return err
	}
	return st.cdb.DeleteDoc(scrapingDB, docID, rev)
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
	body, _, err := st.cdb.GetDoc(scrapingDB, docID)
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
