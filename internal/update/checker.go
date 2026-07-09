package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const githubAPI = "https://api.github.com"

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatestRelease(repo string) (*ghRelease, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		repo = "buscalogo/buscalogo-lauche"
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPI, repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "buscalogo-agent")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("nenhum release publicado em %s", repo)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub API %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func manifestFromRelease(rel *ghRelease) (*Manifest, error) {
	for _, a := range rel.Assets {
		if a.Name == "manifest.json" {
			return fetchManifest(a.BrowserDownloadURL)
		}
	}
	// fallback: montar a partir do .deb no release
	var deb *ghAsset
	for i := range rel.Assets {
		if strings.HasSuffix(rel.Assets[i].Name, "_amd64.deb") {
			deb = &rel.Assets[i]
			break
		}
	}
	if deb == nil {
		return nil, fmt.Errorf("release %s sem manifest.json nem .deb amd64", rel.TagName)
	}
	ver := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	return &Manifest{
		Version: ver,
		Notes:   strings.TrimSpace(rel.Body),
		LinuxAMD64Deb: debAsset{
			URL:  deb.BrowserDownloadURL,
			Name: deb.Name,
		},
	}, nil
}

func fetchManifest(url string) (*Manifest, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest HTTP %d", resp.StatusCode)
	}
	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	if m.Version == "" {
		return nil, fmt.Errorf("manifest sem version")
	}
	return &m, nil
}

func normalizeVersion(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}
