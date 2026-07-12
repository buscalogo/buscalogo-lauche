package scraper

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// SearchHit is a ranked result for P2P SEARCH_RESPONSE (compatible with bl-scraper-server).
type SearchHit struct {
	ID          string         `json:"id"`
	URL         string         `json:"url"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Relevance   int            `json:"relevance"`
	Matches     map[string]any `json:"matches"`
	CreatedBy   any            `json:"created_by"`
	Favicon     string         `json:"favicon,omitempty"`
	Content     map[string]any `json:"content"`
	Metadata    map[string]any `json:"metadata"`
	Source      map[string]any `json:"source"`
}

type relevanceScore struct {
	score   int
	matches map[string][]string
}

func prepareSearchTerms(query string) []string {
	q := strings.ToLower(query)
	var b strings.Builder
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	parts := strings.Fields(b.String())
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) >= 2 {
			out = append(out, p)
		}
	}
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func calculateRelevance(doc StoredDoc, terms []string) relevanceScore {
	rel := relevanceScore{
		matches: map[string][]string{
			"title": {}, "description": {}, "content": {}, "headings": {}, "topics": {},
		},
	}
	if len(terms) == 0 {
		return rel
	}

	headings := append([]string{}, doc.Content.Headings.H1...)
	headings = append(headings, doc.Content.Headings.H2...)
	text := map[string]string{
		"title":       strings.ToLower(doc.Title),
		"description": strings.ToLower(doc.Description),
		"content":     strings.ToLower(doc.Text),
		"headings":    strings.ToLower(strings.Join(headings, " ")),
		"topics":      strings.ToLower(strings.Join(doc.Analysis.Topics, " ")),
	}

	matched := map[string]bool{}
	for _, term := range terms {
		if strings.Contains(text["title"], term) {
			rel.score += 10
			rel.matches["title"] = append(rel.matches["title"], term)
			matched[term] = true
		}
		if strings.Contains(text["description"], term) {
			rel.score += 5
			rel.matches["description"] = append(rel.matches["description"], term)
			matched[term] = true
		}
		if strings.Contains(text["headings"], term) {
			rel.score += 7
			rel.matches["headings"] = append(rel.matches["headings"], term)
			matched[term] = true
		}
		if strings.Contains(text["topics"], term) {
			rel.score += 8
			rel.matches["topics"] = append(rel.matches["topics"], term)
			matched[term] = true
		}
		re := regexp.MustCompile(regexp.QuoteMeta(term))
		n := len(re.FindAllString(text["content"], -1))
		if n > 0 {
			if n > 3 {
				n = 3
			}
			rel.score += n
			rel.matches["content"] = append(rel.matches["content"], term)
			matched[term] = true
		}
	}

	if len(matched) < len(terms) {
		return relevanceScore{score: 0, matches: rel.matches}
	}
	fields := 0
	for _, m := range rel.matches {
		if len(m) > 0 {
			fields++
		}
	}
	rel.score += fields * 2
	return rel
}

func (st *Store) Search(query, queryID, peerID string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 50
	}
	terms := prepareSearchTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}

	docs, err := st.ListResultsFull(100)
	if err != nil {
		return nil, err
	}

	hits := make([]SearchHit, 0)
	for _, doc := range docs {
		rel := calculateRelevance(doc, terms)
		if rel.score <= 0 {
			continue
		}
		paras := doc.Content.Paragraphs
		if len(paras) > 3 {
			paras = paras[:3]
		}
		imgs := doc.Content.Images
		if len(imgs) > 3 {
			imgs = imgs[:3]
		}
		favicon := doc.Content.Favicon
		if favicon == "" {
			if v, ok := doc.Metadata["favicon"].(string); ok {
				favicon = v
			}
		}
		contentType := doc.Analysis.ContentType
		if contentType == "" {
			contentType = "unknown"
		}
		hits = append(hits, SearchHit{
			ID:          doc.ID,
			URL:         doc.URL,
			Title:       doc.Title,
			Description: doc.Description,
			Relevance:   rel.score,
			Matches: map[string]any{
				"title":       rel.matches["title"],
				"description": rel.matches["description"],
				"content":     rel.matches["content"],
				"headings":    rel.matches["headings"],
				"topics":      rel.matches["topics"],
			},
			CreatedBy: doc.CreatedBy,
			Favicon:   favicon,
			Content: map[string]any{
				"headings":   doc.Content.Headings,
				"paragraphs": paras,
				"images":     imgs,
			},
			Metadata: map[string]any{
				"scrapedAt":   doc.CreatedAt,
				"hostname":    doc.Hostname,
				"contentType": contentType,
				"topics":      doc.Analysis.Topics,
				"wordCount":   doc.Analysis.WordCount,
				"signed":      doc.Signature != "",
			},
			Source: map[string]any{
				"peerId":    peerID,
				"peerType":  "scraper_server",
				"queryId":   queryID,
				"createdBy": doc.CreatedBy,
			},
		})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Relevance != hits[j].Relevance {
			return hits[i].Relevance > hits[j].Relevance
		}
		ti, _ := time.Parse(time.RFC3339, hits[i].Metadata["scrapedAt"].(string))
		tj, _ := time.Parse(time.RFC3339, hits[j].Metadata["scrapedAt"].(string))
		return ti.After(tj)
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}
