package version

import (
	"strings"

	"golang.org/x/mod/semver"
)

// Version e Commit são injetados no build via -ldflags.
var (
	Version = "dev"
	Commit  = ""
)

func normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return "v0.0.0"
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// Compare compara duas versões semver (-1 se a < b, 0 se iguais, +1 se a > b).
func Compare(a, b string) int {
	return semver.Compare(normalize(a), normalize(b))
}

// Info retorna metadados da build atual.
func Info() map[string]string {
	ch := "stable"
	return map[string]string{
		"version": Version,
		"commit":  Commit,
		"channel": ch,
	}
}
