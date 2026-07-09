package couchdb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseLooksComplete(t *testing.T) {
	root := t.TempDir()
	if releaseLooksComplete(root) {
		t.Fatal("empty dir should be incomplete")
	}
	mustMkdir(t, filepath.Join(root, "bin"))
	mustWrite(t, filepath.Join(root, "bin", "couchdb"), []byte{0}, 0o755)
	mustMkdir(t, filepath.Join(root, "releases"))
	mustWrite(t, filepath.Join(root, "releases", "start_erl.data"), []byte("14.2.5.14\n"), 0o644)
	mustMkdir(t, filepath.Join(root, "lib"))
	mustMkdir(t, filepath.Join(root, "share"))
	mustMkdir(t, filepath.Join(root, "erts-14.2.5.14"))
	if !releaseLooksComplete(root) {
		t.Fatal("expected complete release tree")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(p, data, mode); err != nil {
		t.Fatal(err)
	}
}
