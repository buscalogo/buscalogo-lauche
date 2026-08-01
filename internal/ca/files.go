package ca

import (
	"bytes"
	"os"
	"path/filepath"
)

// WriteRootFile writes rootPEM to destPath if missing or different.
func WriteRootFile(destPath string, rootPEM []byte) error {
	if len(rootPEM) == 0 {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	if cur, err := os.ReadFile(destPath); err == nil && bytes.Equal(cur, rootPEM) {
		return nil
	}
	return os.WriteFile(destPath, rootPEM, 0o644)
}
