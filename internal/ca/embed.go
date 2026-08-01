package ca

import (
	"embed"
	"strings"
)

//go:embed all:certs
var embeddedCerts embed.FS

// EmbeddedRootPEM returns rootCA.pem from assets/certs if present in the binary.
func EmbeddedRootPEM() ([]byte, bool) {
	b, err := embeddedCerts.ReadFile("certs/rootCA.pem")
	if err != nil || len(b) == 0 || !strings.Contains(string(b), "BEGIN CERTIFICATE") {
		return nil, false
	}
	return b, true
}
