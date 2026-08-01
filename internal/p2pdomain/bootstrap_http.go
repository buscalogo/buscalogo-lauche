package p2pdomain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBootstrapURL is the public static registry directory on buscalogo.com.
const DefaultBootstrapURL = "https://buscalogo.com/registries.json"

type httpBootstrapFile struct {
	UpdatedAt  string `json:"updated_at"`
	Registries []struct {
		YggIP string `json:"ygg_ip"`
		Name  string `json:"name,omitempty"`
		Note  string `json:"note,omitempty"`
	} `json:"registries"`
}

// FetchBootstrapRegistries GETs a static JSON directory of registry Ygg IPs.
// Empty url disables the fetch. Returns normalized IPs (may be empty without error if list empty).
func FetchBootstrapRegistries(ctx context.Context, url string) ([]string, error) {
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
	seen := map[string]bool{}
	out := make([]string, 0, len(f.Registries))
	for _, r := range f.Registries {
		ip := normalizeYggIP(r.YggIP)
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip)
	}
	return out, nil
}
