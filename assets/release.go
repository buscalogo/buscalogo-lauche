package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// HasRelease informa se um release embutido (diretório, ex: couchdb/) existe.
func HasRelease(name string) bool {
	root := filepath.ToSlash(filepath.Join(platformDir(), name))
	entries, err := linuxFS.ReadDir(root)
	if err != nil || len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if e.Name() == "MANIFEST" || e.Name() == ".gitkeep" {
			continue
		}
		return true
	}
	return false
}

// EnsureRelease extrai um release embutido (diretório) para destDir/name.
// Retorna o caminho raiz do release no disco.
func EnsureRelease(name, destDir string) (string, error) {
	embedRoot := filepath.ToSlash(filepath.Join(platformDir(), name))
	if _, err := linuxFS.Open(embedRoot); err != nil {
		return "", fmt.Errorf("release %s não embutido: %w", name, err)
	}

	target := filepath.Join(destDir, name)
	marker := filepath.Join(target, ".buscalogo-release-installed")
	want, err := releaseDigest(embedRoot)
	if err != nil {
		return "", err
	}
	if data, err := os.ReadFile(marker); err == nil && string(data) == want {
		if bin := releaseBinary(target); bin != "" {
			if info, err := os.Stat(bin); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return target, nil
			}
		}
	}

	if err := os.RemoveAll(target); err != nil {
		return "", fmt.Errorf("limpar release antigo: %w", err)
	}
	if err := extractTree(embedRoot, target); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(marker, []byte(want), 0o644); err != nil {
		return "", err
	}
	bin := releaseBinary(target)
	if bin == "" {
		return "", fmt.Errorf("release %s sem bin/couchdb em %s", name, target)
	}
	return target, nil
}

func releaseBinary(releaseRoot string) string {
	if runtime.GOOS == "windows" {
		p := filepath.Join(releaseRoot, "bin", "couchdb.cmd")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	p := filepath.Join(releaseRoot, "bin", "couchdb")
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

func releaseDigest(embedRoot string) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(linuxFS, embedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "MANIFEST" || name == ".gitkeep" {
			return nil
		}
		rel := strings.TrimPrefix(path, embedRoot)
		rel = strings.TrimPrefix(rel, "/")
		io.WriteString(h, rel)
		io.WriteString(h, "\x00")
		data, err := linuxFS.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write(data)
		io.WriteString(h, "\x00")
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extractTree(embedRoot, target string) error {
	return fs.WalkDir(linuxFS, embedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, embedRoot)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return os.MkdirAll(target, 0o755)
		}
		if d.Name() == ".gitkeep" {
			return nil
		}
		out := filepath.Join(target, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := linuxFS.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, "/couchdb") || strings.HasSuffix(rel, "/beam.smp") || strings.Contains(rel, "/bin/") {
			mode = 0o755
		}
		return os.WriteFile(out, data, mode)
	})
}
