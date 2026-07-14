//go:build !registry && !windows

package assets

import (
	"embed"
)

// CouchDB só no Agent Linux (build sem -tags registry).
//
//go:embed all:linux/couchdb
var couchdbFS embed.FS
