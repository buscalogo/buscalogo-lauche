package couchdb

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const bundledReleasePath = "/opt/buscalogo/data/bin/couchdb"

func releaseManifestField(root, key string) string {
	data, err := os.ReadFile(filepath.Join(root, "MANIFEST"))
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func releaseManifestVersion(root string) string {
	return releaseManifestField(root, "COUCHDB_VERSION")
}

func releaseManifestID(root string) string {
	ver := releaseManifestVersion(root)
	if ver == "" {
		return ""
	}
	if codename := releaseManifestField(root, "COUCHDB_CODENAME"); codename != "" {
		return ver + "~" + codename
	}
	return ver
}

func ensureUserRelease(binDir, source string) (string, error) {
	target := filepath.Join(binDir, releaseName)
	if source == "" {
		return target, fmt.Errorf("fonte couchdb vazia")
	}
	srcID := releaseManifestID(source)
	dstID := releaseManifestID(target)
	if isExec(filepath.Join(target, "bin", "couchdb")) && (srcID == "" || srcID == dstID) {
		return target, nil
	}
	if err := os.RemoveAll(target); err != nil {
		return "", fmt.Errorf("limpar couchdb antigo: %w", err)
	}
	if err := copyReleaseTree(source, target); err != nil {
		return "", fmt.Errorf("copiar couchdb de %s: %w", source, err)
	}
	return target, nil
}

func isExec(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func copyReleaseTree(src, dst string) error {
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		mode := info.Mode().Perm()
		if mode&0o111 != 0 {
			mode = 0o755
		}
		outf, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(outf, in); err != nil {
			outf.Close()
			return err
		}
		return outf.Close()
	})
}
