package scraper

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestDocIDForURLStable(t *testing.T) {
	url := "https://example.com/page"
	sum := sha256.Sum256([]byte(url))
	want := "scraping_" + hex.EncodeToString(sum[:16])
	got := docIDForURL(url)
	if got != want {
		t.Fatalf("docIDForURL = %q, want %q", got, want)
	}
	if docIDForURL(url) != docIDForURL(url) {
		t.Fatal("docIDForURL not stable")
	}
}
