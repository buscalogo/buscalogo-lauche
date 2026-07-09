package couchdb

import (
	"fmt"
	"os"
	"os/exec"
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

func releaseLooksComplete(root string) bool {
	if !isExec(filepath.Join(root, "bin", "couchdb")) {
		return false
	}
	for _, rel := range []string{
		"releases/start_erl.data",
		"lib",
		"share",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return false
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "erts-") {
			continue
		}
		p := filepath.Join(root, e.Name())
		info, err := os.Stat(p)
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func ensureUserRelease(binDir, source string) (string, error) {
	target := filepath.Join(binDir, releaseName)
	if source == "" {
		return target, fmt.Errorf("fonte couchdb vazia")
	}
	srcID := releaseManifestID(source)
	dstID := releaseManifestID(target)
	if releaseLooksComplete(target) && (srcID == "" || srcID == dstID) {
		return target, nil
	}
	if err := os.RemoveAll(target); err != nil {
		return "", fmt.Errorf("limpar couchdb antigo: %w", err)
	}
	if err := copyReleaseTree(source, target); err != nil {
		return "", fmt.Errorf("copiar couchdb de %s: %w", source, err)
	}
	if !releaseLooksComplete(target) {
		_ = os.RemoveAll(target)
		return "", fmt.Errorf("cópia incompleta de %s (faltam erts/releases/lib)", source)
	}
	return target, nil
}

func isExec(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// copyReleaseTree preserva symlinks e estrutura Erlang (tar pipe — mais confiável que cp).
func copyReleaseTree(src, dst string) error {
	src = filepath.Clean(src)
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("limpar destino: %w", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	// tar evita problemas com symlinks (data -> /var/lib/couchdb) e permissões mistas.
	cmd := exec.Command("sh", "-c",
		fmt.Sprintf("cd %q && tar cf - . | (cd %q && tar xf -)", src, dst))
	out, err := cmd.CombinedOutput()
	if err != nil {
		// fallback cp -a
		if err2 := os.RemoveAll(dst); err2 != nil {
			return err2
		}
		if err3 := os.MkdirAll(dst, 0o755); err3 != nil {
			return err3
		}
		cp := exec.Command("cp", "-a", "--no-preserve=ownership", filepath.Join(src, "."), dst)
		out2, err4 := cp.CombinedOutput()
		if err4 != nil {
			return fmt.Errorf("tar: %w (%s); cp: %v (%s)", err, strings.TrimSpace(string(out)), err4, strings.TrimSpace(string(out2)))
		}
	}
	return nil
}
