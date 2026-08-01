package ca

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"buscalogo-agent/internal/paths"
)

// loadKnownRegistryIPs reads data/registry/known-registries.json (best-effort).
// Used so CA issue still finds peers when GossipSub status is stale/offline.
func loadKnownRegistryIPs() []string {
	data, err := paths.Data()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(data, "registry", "known-registries.json"))
	if err != nil || len(raw) == 0 {
		return nil
	}
	var doc struct {
		Registries []struct {
			YggIP string `json:"ygg_ip"`
		} `json:"registries"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range doc.Registries {
		ip := strings.TrimSpace(r.YggIP)
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip)
	}
	return out
}

func lastIssuerPath() (string, error) {
	dir, err := paths.Certs()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "last-issuer.url"), nil
}

func loadLastIssuerURL() string {
	path, err := lastIssuerPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func saveLastIssuerURL(u string) error {
	u = strings.TrimSpace(u)
	if u == "" {
		return nil
	}
	path, err := lastIssuerPath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(u+"\n"), 0o644)
}

// loadPreferredIssuerIPs returns Ygg IPs marked ca_issuer in the public bootstrap JSON cache
// or from BUSCALOGO_CA_ISSUERS (comma-separated).
func loadPreferredIssuerIPs() []string {
	if env := strings.TrimSpace(os.Getenv("BUSCALOGO_CA_ISSUERS")); env != "" {
		var out []string
		for _, p := range strings.Split(env, ",") {
			ip := strings.TrimSpace(p)
			if ip != "" {
				out = append(out, ip)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	data, err := paths.Data()
	if err != nil {
		return nil
	}
	// Optional local copy of registries.json with ca_issuer hints.
	raw, err := os.ReadFile(filepath.Join(data, "registry", "registries-bootstrap.json"))
	if err != nil || len(raw) == 0 {
		return nil
	}
	var doc struct {
		Registries []struct {
			YggIP    string `json:"ygg_ip"`
			CAIssuer bool   `json:"ca_issuer"`
		} `json:"registries"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var out []string
	for _, r := range doc.Registries {
		if !r.CAIssuer {
			continue
		}
		ip := strings.TrimSpace(r.YggIP)
		if ip != "" {
			out = append(out, ip)
		}
	}
	return out
}
