//go:build !registry && windows

package assets

import "embed"

// Windows não embute o release CouchDB (~108MB). HasRelease retorna false.
var couchdbFS embed.FS
