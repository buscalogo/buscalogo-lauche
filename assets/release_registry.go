//go:build registry

package assets

import "fmt"

// HasRelease — build registry não embute CouchDB.
func HasRelease(name string) bool { return false }

// EnsureRelease — build registry não embute CouchDB.
func EnsureRelease(name, destDir string) (string, error) {
	return "", fmt.Errorf("release %s não embutido (build -tags registry)", name)
}
