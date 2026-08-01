//go:build unix

package ca

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"buscalogo-agent/internal/system"
)

const systemTrustBasename = "buscalogo-root-ca"

type trustTarget struct {
	filename string
	update   []string
}

func detectTrustTarget() *trustTarget {
	switch {
	case dirExists("/etc/pki/ca-trust/source/anchors"):
		return &trustTarget{
			filename: filepath.Join("/etc/pki/ca-trust/source/anchors", systemTrustBasename+".pem"),
			update:   []string{"update-ca-trust", "extract"},
		}
	case dirExists("/usr/local/share/ca-certificates") || dirExists("/usr/share/ca-certificates"):
		_ = os.MkdirAll("/usr/local/share/ca-certificates", 0o755)
		return &trustTarget{
			filename: filepath.Join("/usr/local/share/ca-certificates", systemTrustBasename+".crt"),
			update:   []string{"update-ca-certificates"},
		}
	case dirExists("/etc/ca-certificates/trust-source/anchors"):
		return &trustTarget{
			filename: filepath.Join("/etc/ca-certificates/trust-source/anchors", systemTrustBasename+".crt"),
			update:   []string{"trust", "extract-compat"},
		}
	case dirExists("/usr/share/pki/trust/anchors"):
		return &trustTarget{
			filename: filepath.Join("/usr/share/pki/trust/anchors", systemTrustBasename+".pem"),
			update:   []string{"update-ca-certificates"},
		}
	default:
		return nil
	}
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// SystemTrustInstalled reports whether the BuscaLogo root appears installed.
func SystemTrustInstalled() bool {
	t := detectTrustTarget()
	if t == nil {
		return false
	}
	_, err := os.Stat(t.filename)
	return err == nil
}

type systemAppender interface {
	Write(p []byte) (int, error)
}

// InstallSystemTrust installs rootPEM into the OS trust store (requires privilege).
func InstallSystemTrust(buf systemAppender, rootPEM []byte) error {
	if len(rootPEM) == 0 {
		return fmt.Errorf("root PEM vazio")
	}
	t := detectTrustTarget()
	if t == nil {
		return fmt.Errorf("trust store do sistema não suportado nesta distro — baixe rootCA.pem e instale manualmente")
	}
	tmp, err := os.CreateTemp("", "buscalogo-root-*.crt")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(rootPEM); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if _, err := system.RunPrivileged(buf, "cp", "-f", tmpPath, t.filename); err != nil {
		return fmt.Errorf("instalar âncora CA: %w", err)
	}
	_, _ = system.RunPrivileged(buf, "chmod", "644", t.filename)
	if len(t.update) > 0 {
		if _, err := system.RunPrivileged(buf, t.update[0], t.update[1:]...); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(t.update, " "), err)
		}
	}
	// Snap/deb Firefox: usar CA do SO (sem depender de certutil).
	if _, err := InstallFirefoxEnterpriseRoots(buf); err != nil && buf != nil {
		_, _ = fmt.Fprintf(buf, "ca: firefox enterprise_roots (após SO): %v\n", err)
	}
	return nil
}

// UninstallSystemTrust removes the BuscaLogo root from the OS trust store.
func UninstallSystemTrust(buf systemAppender) error {
	t := detectTrustTarget()
	if t == nil {
		return fmt.Errorf("trust store não suportado")
	}
	if _, err := os.Stat(t.filename); err != nil {
		return nil
	}
	if _, err := system.RunPrivileged(buf, "rm", "-f", t.filename); err != nil {
		return err
	}
	if len(t.update) > 0 {
		_, _ = system.RunPrivileged(buf, t.update[0], t.update[1:]...)
	}
	return nil
}

// EnsureRootFile is an alias for WriteRootFile.
func EnsureRootFile(destPath string, rootPEM []byte) error {
	return WriteRootFile(destPath, rootPEM)
}
