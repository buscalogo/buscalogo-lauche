//go:build windows

package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// machineInstallDataDir returns ProgramData\BuscaLogo when the binary lives under Program Files\BuscaLogo.
func machineInstallDataDir(exe string) (string, bool) {
	dir := filepath.Clean(filepath.Dir(exe))
	base := strings.ToLower(filepath.Base(dir))
	if base != "buscalogo" {
		return "", false
	}
	parent := strings.ToLower(filepath.Base(filepath.Dir(dir)))
	if parent != "program files" && parent != "program files (x86)" {
		return "", false
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "BuscaLogo"), true
}

// IsProgramFilesInstall reports whether this binary is under Program Files\BuscaLogo.
func IsProgramFilesInstall() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return false
	}
	_, ok := machineInstallDataDir(exe)
	return ok
}
