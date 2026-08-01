//go:build unix

package ca

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"buscalogo-agent/internal/system"
)

const (
	firefoxEnterprisePref = `user_pref("security.enterprise_roots.enabled", true);
`
	firefoxPoliciesJSON = `{
  "policies": {
    "Certificates": {
      "ImportEnterpriseRoots": true
    }
  }
}
`
)

// InstallFirefoxEnterpriseRoots makes Firefox (incl. Ubuntu Snap) trust the OS CA store.
// Snap Firefox ignores /etc/ssl by default unless enterprise_roots is enabled.
// Writes /etc/firefox/policies/policies.json (privileged) and per-profile user.js (no root).
func InstallFirefoxEnterpriseRoots(buf systemAppender) (touched []string, err error) {
	// System policy — used by deb and snap (snap plugs etc-firefox → /etc/firefox).
	polDir := "/etc/firefox/policies"
	polPath := filepath.Join(polDir, "policies.json")
	if err := writeFirefoxPolicies(buf, polDir, polPath); err != nil {
		logf(buf, "ca: firefox policies: %v\n", err)
	} else {
		touched = append(touched, polPath)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		if len(touched) == 0 {
			return nil, err
		}
		return touched, nil
	}
	for _, profile := range firefoxProfileDirs(home) {
		uj := filepath.Join(profile, "user.js")
		if err := ensureFirefoxUserJS(uj); err != nil {
			logf(buf, "ca: firefox user.js %s: %v\n", profile, err)
			continue
		}
		touched = append(touched, uj)
		logf(buf, "ca: firefox enterprise_roots em %s\n", profile)
	}
	if len(touched) == 0 {
		return nil, fmt.Errorf("nenhum perfil Firefox encontrado e policies do sistema falharam")
	}
	return touched, nil
}

func writeFirefoxPolicies(buf systemAppender, polDir, polPath string) error {
	// Merge if file exists with other policies.
	merged := map[string]any{
		"policies": map[string]any{
			"Certificates": map[string]any{
				"ImportEnterpriseRoots": true,
			},
		},
	}
	if raw, err := os.ReadFile(polPath); err == nil && len(raw) > 0 {
		var existing map[string]any
		if json.Unmarshal(raw, &existing) == nil && existing != nil {
			merged = mergeFirefoxPolicies(existing)
		}
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	tmp, err := os.CreateTemp("", "buscalogo-firefox-policies-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if _, err := system.RunPrivileged(buf, "mkdir", "-p", polDir); err != nil {
		// fallback: try without privilege if already writable
		if mkErr := os.MkdirAll(polDir, 0o755); mkErr != nil {
			return fmt.Errorf("mkdir policies: %w", err)
		}
	}
	if _, err := system.RunPrivileged(buf, "cp", "-f", tmpPath, polPath); err != nil {
		if wErr := os.WriteFile(polPath, out, 0o644); wErr != nil {
			return fmt.Errorf("escrever policies: %w", err)
		}
	}
	_, _ = system.RunPrivileged(buf, "chmod", "644", polPath)
	return nil
}

func mergeFirefoxPolicies(existing map[string]any) map[string]any {
	policies, _ := existing["policies"].(map[string]any)
	if policies == nil {
		policies = map[string]any{}
		existing["policies"] = policies
	}
	certs, _ := policies["Certificates"].(map[string]any)
	if certs == nil {
		certs = map[string]any{}
		policies["Certificates"] = certs
	}
	certs["ImportEnterpriseRoots"] = true
	return existing
}

func ensureFirefoxUserJS(path string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(b)
	if strings.Contains(text, "security.enterprise_roots.enabled") {
		// Force true even if previously false
		lines := strings.Split(text, "\n")
		var out []string
		done := false
		for _, line := range lines {
			if strings.Contains(line, "security.enterprise_roots.enabled") {
				if !done {
					out = append(out, strings.TrimRight(firefoxEnterprisePref, "\n"))
					done = true
				}
				continue
			}
			out = append(out, line)
		}
		if !done {
			out = append(out, strings.TrimRight(firefoxEnterprisePref, "\n"))
		}
		return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
	}
	if len(text) > 0 && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(path, []byte(text+firefoxEnterprisePref), 0o644)
}

func firefoxProfileDirs(home string) []string {
	var out []string
	seen := map[string]bool{}
	globs := []string{
		filepath.Join(home, ".mozilla", "firefox", "*", "cert9.db"),
		filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox", "*", "cert9.db"),
		filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox", "*", "cert9.db"),
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			dir := filepath.Dir(m)
			base := filepath.Base(dir)
			if base == "Crash Reports" || base == "Pending Pings" || strings.HasPrefix(base, "Profile") && !fileExists(filepath.Join(dir, "prefs.js")) {
				// keep profiles that have cert9.db already from glob
			}
			if seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
