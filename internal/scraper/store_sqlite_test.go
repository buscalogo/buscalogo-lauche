package scraper

import (
	"path/filepath"
	"testing"
)

func TestSQLiteStoreSaveLookupSearch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	st, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	task := &Task{ID: "t1", URL: "https://example.com/page", Hostname: "example.com"}
	content := PageContent{
		URL:         task.URL,
		Title:       "Receitas de bolo",
		Description: "como fazer bolo",
		MainText:    "bolo de chocolate receita fácil",
	}
	content.Headings.H1 = []string{"Bolo"}
	result := ScrapeResult{
		TaskID:   task.ID,
		URL:      task.URL,
		Content:  content,
		Analysis: Analysis{Topics: []string{"culinaria"}, WordCount: 10, ContentType: "article"},
		Metadata: map[string]any{"favicon": "https://example.com/f.ico"},
	}
	if err := st.Save(task, result, 7); err != nil {
		t.Fatalf("save: %v", err)
	}

	lu, err := st.Lookup(task.URL)
	if err != nil || !lu.Indexed {
		t.Fatalf("lookup: %+v %v", lu, err)
	}
	if lu.Title != "Receitas de bolo" {
		t.Fatalf("title=%q", lu.Title)
	}

	sites, err := st.ListSites()
	if err != nil || len(sites) != 1 || sites[0].Hostname != "example.com" {
		t.Fatalf("sites: %+v %v", sites, err)
	}

	hits, err := st.Search("bolo receita", "q1", "peer1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected search hits")
	}

	n, err := st.DeleteByHostname("example.com")
	if err != nil || n != 1 {
		t.Fatalf("delete host: %d %v", n, err)
	}
	lu2, _ := st.Lookup(task.URL)
	if lu2.Indexed {
		t.Fatal("still indexed after delete")
	}
}
