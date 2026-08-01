package p2pdomain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"buscalogo-agent/internal/paths"
)

// DefaultBootstrapURL is the public static registry directory on buscalogo.com.
const DefaultBootstrapURL = "https://buscalogo.com/registries.json"

// BootstrapEntry is one row from registries.json.
type BootstrapEntry struct {
	YggIP    string
	Name     string
	Note     string
	CAIssuer bool
}

type httpBootstrapFile struct {
	UpdatedAt  string `json:"updated_at"`
	Registries []struct {
		YggIP    string `json:"ygg_ip"`
		Name     string `json:"name,omitempty"`
		Note     string `json:"note,omitempty"`
		CAIssuer bool   `json:"ca_issuer,omitempty"`
	} `json:"registries"`
}

// FetchBootstrapRegistries GETs a static JSON directory of registry Ygg IPs.
// Empty url disables the fetch. Returns normalized IPs (may be empty without error if list empty).
func FetchBootstrapRegistries(ctx context.Context, url string) ([]string, error) {
	entries, err := FetchBootstrapDirectory(ctx, url)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.YggIP)
	}
	return out, nil
}

// FetchBootstrapDirectory returns full bootstrap rows (incl. ca_issuer hints) and caches them locally.
func FetchBootstrapDirectory(ctx context.Context, url string) ([]BootstrapEntry, error) {
	url = strings.TrimSpace(url)
	if url == "" || url == "off" || url == "-" {
		return nil, nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "buscalogo-registry/bootstrap")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var f httpBootstrapFile
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("JSON inválido: %w", err)
	}
	_ = cacheBootstrapFile(body)
	seen := map[string]bool{}
	out := make([]BootstrapEntry, 0, len(f.Registries))
	for _, r := range f.Registries {
		ip := normalizeYggIP(r.YggIP)
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, BootstrapEntry{
			YggIP:    ip,
			Name:     strings.TrimSpace(r.Name),
			Note:     strings.TrimSpace(r.Note),
			CAIssuer: r.CAIssuer,
		})
	}
	return out, nil
}

func cacheBootstrapFile(raw []byte) error {
	data, err := paths.Data()
	if err != nil {
		return err
	}
	dir := filepath.Join(data, "registry")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "registries-bootstrap.json"), raw, 0o644)
}
