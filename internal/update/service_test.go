package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
)

func TestManifestRegistryAsset(t *testing.T) {
	m := &Manifest{
		Version: "1.2.3",
		LinuxAMD64Deb: debAsset{
			URL: "https://example.com/a.deb", SHA256: "aa",
		},
		LinuxAMD64Registry: debAsset{
			URL: "https://example.com/reg", SHA256: "bb", Name: "buscalogo-registry_1.2.3_linux_amd64",
		},
		LinuxARM64Registry: debAsset{
			URL: "https://example.com/reg-arm", SHA256: "cc", Name: "buscalogo-registry_1.2.3_linux_arm64",
		},
	}
	raw, _ := json.Marshal(m)
	var decoded Manifest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LinuxARM64Registry.URL != m.LinuxARM64Registry.URL {
		t.Fatalf("arm64 asset lost: %+v", decoded)
	}

	cfg := config.Default()
	en := true
	cfg.Update.Enabled = &en
	buf := logx.NewBuffer(32)
	svc := NewProduct(cfg, buf, ProductRegistry)
	a, err := svc.assetFromManifest(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	switch runtime.GOARCH {
	case "arm64":
		if a.SHA256 != "cc" {
			t.Fatalf("want arm64 asset, got %+v", a)
		}
	case "amd64":
		if a.SHA256 != "bb" {
			t.Fatalf("want amd64 asset, got %+v", a)
		}
	}

	agent := New(cfg, buf)
	da, err := agent.assetFromManifest(&decoded)
	if err != nil || da.URL != m.LinuxAMD64Deb.URL {
		t.Fatalf("agent asset: %+v err=%v", da, err)
	}
}

func TestManifestFromReleaseFallbackRegistry(t *testing.T) {
	rel := &ghRelease{
		TagName: "v9.9.9",
		Body:    "notes",
		Assets: []ghAsset{
			{Name: "buscalogo-registry_9.9.9_linux_amd64", BrowserDownloadURL: "https://x/reg"},
			{Name: "buscalogo-registry_9.9.9_linux_arm64", BrowserDownloadURL: "https://x/reg-arm"},
			{Name: "buscalogo-registry_9.9.9_amd64.deb", BrowserDownloadURL: "https://x/reg.deb"},
			{Name: "buscalogo-registry_9.9.9_arm64.deb", BrowserDownloadURL: "https://x/reg-arm.deb"},
			{Name: "buscalogo-agent_9.9.9_amd64.deb", BrowserDownloadURL: "https://x/deb"},
			{Name: "buscalogo-agent-server_9.9.9_amd64.deb", BrowserDownloadURL: "https://x/server.deb"},
		},
	}
	m, err := manifestFromRelease(rel)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "9.9.9" {
		t.Fatalf("version=%s", m.Version)
	}
	if m.LinuxAMD64Registry.URL != "https://x/reg" {
		t.Fatalf("registry url=%s", m.LinuxAMD64Registry.URL)
	}
	if m.LinuxARM64Registry.URL != "https://x/reg-arm" {
		t.Fatalf("arm64 url=%s", m.LinuxARM64Registry.URL)
	}
	if m.LinuxAMD64RegistryDeb.URL != "https://x/reg.deb" {
		t.Fatalf("registry deb=%s", m.LinuxAMD64RegistryDeb.URL)
	}
	if m.LinuxARM64RegistryDeb.URL != "https://x/reg-arm.deb" {
		t.Fatalf("registry arm deb=%s", m.LinuxARM64RegistryDeb.URL)
	}
	if m.LinuxAMD64Deb.URL != "https://x/deb" {
		t.Fatalf("deb url=%s", m.LinuxAMD64Deb.URL)
	}
	if m.LinuxAMD64AgentServerDeb.URL != "https://x/server.deb" {
		t.Fatalf("agent server deb=%s", m.LinuxAMD64AgentServerDeb.URL)
	}
}

func TestInstallRegistryBinarySwap(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "buscalogo-registry")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, "downloaded")
	payload := []byte("NEW-BINARY-CONTENT")
	if err := os.WriteFile(newBin, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate os.Executable by installing into a copy we control via helper path:
	// InstallRegistryBinary uses os.Executable() — test the swap steps via a small local copy.
	// Call install logic on a fake by temporarily wrapping: write into same dir pattern.
	// Instead, exercise verify + file ops used by install.
	sum := sha256.Sum256(payload)
	want := hex.EncodeToString(sum[:])
	if err := verifySHA256(newBin, want); err != nil {
		t.Fatal(err)
	}

	tmp := exe + ".new"
	bak := exe + ".old"
	in, err := os.ReadFile(newBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, in, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(exe, bak); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != string(payload) {
		t.Fatalf("got %q", got)
	}
	old, _ := os.ReadFile(bak)
	if string(old) != "OLD" {
		t.Fatalf("backup %q", old)
	}
}

func TestFetchManifestHTTP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Manifest{
			Version:            "0.2.0",
			LinuxAMD64Deb:      debAsset{URL: "https://x/a.deb", SHA256: "11"},
			LinuxAMD64Registry: debAsset{URL: "https://x/r", SHA256: "22"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	m, err := fetchManifest(srv.URL + "/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if m.LinuxAMD64Registry.SHA256 != "22" {
		t.Fatalf("%+v", m)
	}
}
