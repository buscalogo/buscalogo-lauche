//go:build !windows

package assets

import "embed"

//go:embed icons/logo.png
var logoBytes []byte

// Núcleo embutido em builds não-Windows (Agent + Registry).
// CouchDB fica em couchdb_embed.go (!registry && !windows) — ~108MB a menos no seed Windows.
//
//go:embed linux/yggdrasil linux/coredns linux/MANIFEST
var platformFS embed.FS
