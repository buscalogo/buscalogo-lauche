//go:build unix

package ca

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	browserTrustNick = "BuscaLogo Root CA"
	caEnvFileName    = "99-buscalogo-ca.conf"
	caShellSnippet   = `# BuscaLogo mesh CA (HTTPS .bl/.lo)
[ -f "$HOME/.config/buscalogo/ca.env" ] && . "$HOME/.config/buscalogo/ca.env"
`
)

// UserTrustResult summarizes user-space CA installs (browsers + IDEs/CLIs).
type UserTrustResult struct {
	Browsers []string `json:"browsers,omitempty"`
	Apps     []string `json:"apps,omitempty"`
	EnvFile  string   `json:"env_file,omitempty"`
	CertFile string   `json:"cert_file,omitempty"`
	Bundle   string   `json:"bundle,omitempty"`
}

// nssDBDirs returns user NSS database directories (browsers + Electron/Chromium IDEs).
func nssDBDirs(home string) []string {
	if home == "" {
		return nil
	}
	pki := filepath.Join(home, ".pki", "nssdb")
	seen := map[string]bool{pki: true}
	dbs := []string{pki}

	globs := []string{
		filepath.Join(home, ".mozilla", "firefox", "*", "cert9.db"),
		filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox", "*", "cert9.db"),
		filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox", "*", "cert9.db"),
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			dir := filepath.Dir(m)
			if seen[dir] {
				continue
			}
			seen[dir] = true
			dbs = append(dbs, dir)
		}
	}

	// IDEs / Electron: walk shallow config trees for cert9.db
	roots := []string{
		filepath.Join(home, ".config", "Code"),
		filepath.Join(home, ".config", "Code - OSS"),
		filepath.Join(home, ".config", "Cursor"),
		filepath.Join(home, ".config", "Trae"),
		filepath.Join(home, ".config", "trae"),
		filepath.Join(home, ".config", "Trae CN"),
		filepath.Join(home, ".cursor"),
		filepath.Join(home, ".vscode"),
	}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			if d.IsDir() {
				// limit depth
				rel, _ := filepath.Rel(root, path)
				if rel != "." && strings.Count(rel, string(os.PathSeparator)) > 3 {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() != "cert9.db" {
				return nil
			}
			dir := filepath.Dir(path)
			if seen[dir] {
				return nil
			}
			seen[dir] = true
			dbs = append(dbs, dir)
			return nil
		})
	}
	return dbs
}

// BrowserTrustInstalled reports whether at least one user NSS DB has the BuscaLogo root.
func BrowserTrustInstalled() bool {
	return len(BrowserTrustProfiles()) > 0
}

// BrowserTrustProfiles returns NSS DB paths that already contain the BuscaLogo root.
func BrowserTrustProfiles() []string {
	certutil, err := exec.LookPath("certutil")
	if err != nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, db := range nssDBDirs(home) {
		if _, err := os.Stat(filepath.Join(db, "cert9.db")); err != nil {
			continue
		}
		cmd := exec.Command(certutil, "-d", "sql:"+db, "-L", "-n", browserTrustNick)
		if err := cmd.Run(); err == nil {
			out = append(out, db)
		}
	}
	return out
}

// AppTrustInstalled reports whether IDE/CLI env hooks for the mesh CA are present.
func AppTrustInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	cert := userRootCAPath(home)
	if _, err := os.Stat(cert); err != nil {
		return false
	}
	envd := filepath.Join(home, ".config", "environment.d", caEnvFileName)
	if _, err := os.Stat(envd); err == nil {
		return true
	}
	caEnv := filepath.Join(home, ".config", "buscalogo", "ca.env")
	_, err = os.Stat(caEnv)
	return err == nil
}

func userRootCAPath(home string) string {
	return filepath.Join(home, ".buscalogo", "data", "certs", "rootCA.pem")
}

func userBundlePath(home string) string {
	return filepath.Join(home, ".buscalogo", "data", "certs", "ca-bundle.crt")
}

// InstallBrowserTrust installs rootPEM into user NSS DBs (Firefox / Chromium / Brave / IDEs).
func InstallBrowserTrust(buf systemAppender, rootPEM []byte) (installed []string, err error) {
	res, err := InstallUserTrust(buf, rootPEM)
	return res.Browsers, err
}

// InstallUserTrust installs the mesh root for browsers, Electron IDEs (VS Code/Cursor/Trae),
// Node tooling (NODE_EXTRA_CA_CERTS) and OpenSSL-based apps (SSL_CERT_FILE bundle).
// Zed and most native apps pick up the system store; Electron/Node need the env hooks.
func InstallUserTrust(buf systemAppender, rootPEM []byte) (UserTrustResult, error) {
	var res UserTrustResult
	if len(rootPEM) == 0 {
		return res, fmt.Errorf("root PEM vazio")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return res, err
	}

	certPath, err := writeUserRootCA(home, rootPEM)
	if err != nil {
		return res, err
	}
	res.CertFile = certPath
	logf(buf, "ca: rootCA em %s\n", certPath)

	if bundle, err := writeUserCABundle(home, rootPEM); err == nil {
		res.Bundle = bundle
		logf(buf, "ca: bundle OpenSSL em %s\n", bundle)
	}

	if envFile, apps, err := installAppEnvHooks(home, certPath, res.Bundle); err != nil {
		logf(buf, "ca: env IDEs/CLIs: %v\n", err)
	} else {
		res.EnvFile = envFile
		res.Apps = append(res.Apps, apps...)
	}

	if ff, err := InstallFirefoxEnterpriseRoots(buf); err != nil {
		logf(buf, "ca: firefox enterprise_roots: %v\n", err)
	} else {
		res.Apps = append(res.Apps, ff...)
	}

	browsers, nssErr := installNSSTrust(buf, home, rootPEM)
	res.Browsers = browsers
	if nssErr != nil && len(browsers) == 0 && len(res.Apps) == 0 {
		return res, nssErr
	}
	if nssErr != nil {
		logf(buf, "ca: NSS: %v\n", nssErr)
	}
	return res, nil
}

func writeUserRootCA(home string, rootPEM []byte) (string, error) {
	dir := filepath.Join(home, ".buscalogo", "data", "certs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "rootCA.pem")
	if err := os.WriteFile(path, rootPEM, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func writeUserCABundle(home string, rootPEM []byte) (string, error) {
	dir := filepath.Join(home, ".buscalogo", "data", "certs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := userBundlePath(home)
	var parts []byte
	for _, sys := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/cert.pem",
	} {
		if b, err := os.ReadFile(sys); err == nil && len(b) > 0 {
			parts = append(parts, b...)
			if parts[len(parts)-1] != '\n' {
				parts = append(parts, '\n')
			}
			break
		}
	}
	parts = append(parts, rootPEM...)
	if err := os.WriteFile(path, parts, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func installAppEnvHooks(home, certPath, bundlePath string) (envFile string, apps []string, err error) {
	cfgDir := filepath.Join(home, ".config", "buscalogo")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return "", nil, err
	}
	caEnv := filepath.Join(cfgDir, "ca.env")
	var b strings.Builder
	b.WriteString("# Gerado pelo BuscaLogo Agent — CA da mesh para IDEs/CLIs\n")
	b.WriteString("# Cursor, VS Code, Trae, Node, Python requests, etc.\n")
	fmt.Fprintf(&b, "export NODE_EXTRA_CA_CERTS=%q\n", certPath)
	if bundlePath != "" {
		fmt.Fprintf(&b, "export SSL_CERT_FILE=%q\n", bundlePath)
		fmt.Fprintf(&b, "export REQUESTS_CA_BUNDLE=%q\n", bundlePath)
		fmt.Fprintf(&b, "export CURL_CA_BUNDLE=%q\n", bundlePath)
	}
	if err := os.WriteFile(caEnv, []byte(b.String()), 0o644); err != nil {
		return "", nil, err
	}
	apps = append(apps, caEnv)

	// systemd user environment (GNOME/KDE session, many Electron apps launched from desktop)
	envdDir := filepath.Join(home, ".config", "environment.d")
	_ = os.MkdirAll(envdDir, 0o755)
	envd := filepath.Join(envdDir, caEnvFileName)
	var ed strings.Builder
	fmt.Fprintf(&ed, "NODE_EXTRA_CA_CERTS=%s\n", certPath)
	if bundlePath != "" {
		fmt.Fprintf(&ed, "SSL_CERT_FILE=%s\n", bundlePath)
		fmt.Fprintf(&ed, "REQUESTS_CA_BUNDLE=%s\n", bundlePath)
		fmt.Fprintf(&ed, "CURL_CA_BUNDLE=%s\n", bundlePath)
	}
	if err := os.WriteFile(envd, []byte(ed.String()), 0o644); err != nil {
		return caEnv, apps, err
	}
	apps = append(apps, envd)
	envFile = envd

	ensureShellSnippet(home, &apps)

	// Hint files for known IDEs (documentation only + stable cert path)
	for _, rel := range []string{
		".config/Cursor",
		".config/Code",
		".config/Code - OSS",
		".config/Trae",
		".config/trae",
		".config/zed",
		".config/Zed",
	} {
		dir := filepath.Join(home, rel)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		hint := filepath.Join(dir, "buscalogo-ca.txt")
		msg := fmt.Sprintf("BuscaLogo root CA: %s\nNODE_EXTRA_CA_CERTS / SSL_CERT_FILE via ~/.config/environment.d/%s\nReinicie o app após instalar a CA.\n", certPath, caEnvFileName)
		if err := os.WriteFile(hint, []byte(msg), 0o644); err == nil {
			apps = append(apps, hint)
		}
	}
	return envFile, apps, nil
}

func ensureShellSnippet(home string, apps *[]string) {
	for _, name := range []string{".profile", ".bashrc", ".zshrc"} {
		path := filepath.Join(home, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(b)
		if strings.Contains(text, ".config/buscalogo/ca.env") {
			continue
		}
		sep := "\n"
		if len(text) > 0 && !strings.HasSuffix(text, "\n") {
			sep = "\n\n"
		}
		if err := os.WriteFile(path, []byte(text+sep+caShellSnippet), 0o644); err == nil && apps != nil {
			*apps = append(*apps, path+"#snippet")
		}
	}
}

func installNSSTrust(buf systemAppender, home string, rootPEM []byte) ([]string, error) {
	certutil, err := exec.LookPath("certutil")
	if err != nil {
		return nil, fmt.Errorf("certutil não encontrado (instale libnss3-tools) — Firefox/Brave/Chromium precisam da CA no NSS")
	}
	tmp, err := os.CreateTemp("", "buscalogo-root-*.pem")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(rootPEM); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	_ = tmp.Close()

	pki := filepath.Join(home, ".pki", "nssdb")
	_ = os.MkdirAll(pki, 0o700)
	if _, err := os.Stat(filepath.Join(pki, "cert9.db")); err != nil {
		_ = exec.Command(certutil, "-d", "sql:"+pki, "-N", "--empty-password").Run()
	}

	var installed []string
	for _, db := range nssDBDirs(home) {
		db = filepath.Clean(db)
		if db == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(db, "cert9.db")); err != nil && db != pki {
			continue
		}
		sqlDB := "sql:" + db
		_ = exec.Command(certutil, "-d", sqlDB, "-D", "-n", browserTrustNick).Run()
		cmd := exec.Command(certutil, "-d", sqlDB, "-A", "-t", "C,,", "-n", browserTrustNick, "-i", tmpPath)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			logf(buf, "ca: NSS %s: %v (%s)\n", db, runErr, strings.TrimSpace(string(out)))
			continue
		}
		installed = append(installed, db)
		logf(buf, "ca: rootCA instalada no NSS %s\n", db)
	}
	if len(installed) == 0 {
		return nil, fmt.Errorf("nenhum perfil NSS atualizado — instale libnss3-tools e reinicie o navegador")
	}
	return installed, nil
}

func logf(buf systemAppender, format string, args ...any) {
	if buf == nil {
		return
	}
	_, _ = fmt.Fprintf(buf, format, args...)
}
