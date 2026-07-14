package scraper

import (
	"fmt"
	"path/filepath"
	"runtime"

	"buscalogo-agent/internal/couchdb"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
)

// OpenScrapeStore escolhe o backend do índice:
//   - Windows → SQLite em data/scrape/index.sqlite
//   - Linux/macOS → CouchDB (se enabled)
func OpenScrapeStore(cdb *couchdb.Service, buf *logx.Buffer) (ScrapeStore, error) {
	if runtime.GOOS == "windows" {
		data, err := paths.Data()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(data, "scrape", "index.sqlite")
		st, err := OpenSQLiteStore(path)
		if err != nil {
			return nil, fmt.Errorf("abrir índice SQLite: %w", err)
		}
		if buf != nil {
			buf.Infof("scraper", "índice SQLite: %s", path)
		}
		return st, nil
	}
	if cdb != nil && cdb.Enabled() {
		if buf != nil {
			buf.Infof("scraper", "índice CouchDB (%s* + %s)", hostDBPrefix, scrapingDBLegacy)
		}
		return NewStore(cdb), nil
	}
	return nil, nil
}
