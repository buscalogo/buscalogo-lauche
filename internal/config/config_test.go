package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeJSON(t *testing.T) {
	c := Default()
	if c.Web.Port != 80 {
		t.Fatalf("default web port: %d", c.Web.Port)
	}
	if len(c.Sites) != 1 {
		t.Fatalf("default sites: %d", len(c.Sites))
	}

	if err := c.MergeJSON([]byte(`{"node":{"name":"Teste"}}`)); err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}
	if c.Web.Port != 80 {
		t.Fatalf("web port perdido: %d", c.Web.Port)
	}
	if len(c.Sites) != 1 {
		t.Fatalf("sites perdido: %d", len(c.Sites))
	}
	if c.Node.Name != "Teste" {
		t.Fatalf("node name não atualizado: %s", c.Node.Name)
	}
}

func TestMergeJSONAfterLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	orig := Default()
	orig.path = p
	if err := orig.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := loadAtPath(p)
	if err != nil {
		t.Fatalf("loadAtPath: %v", err)
	}
	if len(loaded.Sites) != 1 {
		t.Fatalf("loaded sites: %d", len(loaded.Sites))
	}
	if err := loaded.MergeJSON([]byte(`{"node":{"name":"Teste"}}`)); err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}
	if loaded.Web.Port != 80 {
		t.Fatalf("web port perdido: %d", loaded.Web.Port)
	}
	if len(loaded.Sites) != 1 {
		t.Fatalf("sites perdido: %d", len(loaded.Sites))
	}
	_ = os.Remove(p)
}

func TestMergeJSONP2P(t *testing.T) {
	c := Default()
	c.P2P.Enabled = BoolPtr(false)
	if err := c.MergeJSON([]byte(`{"p2p":{"enabled":true,"signaling_urls":["ws://localhost:3001"],"max_results_per_query":25}}`)); err != nil {
		t.Fatalf("MergeJSON p2p: %v", err)
	}
	if !c.P2PEnabled() {
		t.Fatal("p2p.enabled não aplicado")
	}
	if len(c.P2P.SignalingURLs) != 1 || c.P2P.SignalingURLs[0] != "ws://localhost:3001" {
		t.Fatalf("signaling_urls: %v", c.P2P.SignalingURLs)
	}
	if c.P2P.MaxResultsPerQuery != 25 {
		t.Fatalf("max_results_per_query: %d", c.P2P.MaxResultsPerQuery)
	}
}

func TestSnapshotIncludesP2P(t *testing.T) {
	c := Default()
	c.P2P.SignalingURLs = []string{"wss://api.buscalogo.com", "ws://localhost:3001"}
	snap := c.Snapshot()
	if !snap.P2P.EnabledOrDefault() {
		t.Fatal("snapshot p2p.enabled missing")
	}
	if len(snap.P2P.SignalingURLs) != 2 {
		t.Fatalf("snapshot signaling_urls: %v", snap.P2P.SignalingURLs)
	}
}

func TestP2PYAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	c.path = filepath.Join(dir, "config.yaml")
	c.P2P.SignalingURLs = []string{"wss://api.buscalogo.com"}
	yamlText, err := c.P2PYAML()
	if err != nil {
		t.Fatalf("P2PYAML: %v", err)
	}
	c.P2P.Enabled = BoolPtr(false)
	c.P2P.SignalingURLs = nil
	if err := c.ApplyP2PYAML(yamlText); err != nil {
		t.Fatalf("ApplyP2PYAML: %v", err)
	}
	if !c.P2PEnabled() {
		t.Fatal("p2p.enabled not restored from yaml")
	}
	if len(c.P2P.SignalingURLs) != 1 {
		t.Fatalf("signaling_urls: %v", c.P2P.SignalingURLs)
	}
}

func TestResetP2P(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	c.path = filepath.Join(dir, "config.yaml")
	c.P2P.Enabled = BoolPtr(false)
	c.P2P.SignalingURLs = []string{"ws://custom:9999"}
	c.P2P.MaxResultsPerQuery = 10
	if err := c.ResetP2P(); err != nil {
		t.Fatalf("ResetP2P: %v", err)
	}
	def := DefaultP2P()
	if c.P2PEnabled() != def.EnabledOrDefault() {
		t.Fatalf("enabled: %v", c.P2PEnabled())
	}
	if len(c.P2P.SignalingURLs) != 1 || c.P2P.SignalingURLs[0] != def.SignalingURLs[0] {
		t.Fatalf("urls: %v", c.P2P.SignalingURLs)
	}
	if c.P2P.MaxResultsPerQuery != def.MaxResultsPerQuery {
		t.Fatalf("max: %d", c.P2P.MaxResultsPerQuery)
	}
}

func TestP2PEnabledDefaultWhenOmittedInYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	cfg := `node:
  name: Test
p2p:
  signaling_urls:
    - wss://api.buscalogo.com
  max_results_per_query: 50
`
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadAtPath(p)
	if err != nil {
		t.Fatalf("loadAtPath: %v", err)
	}
	if !loaded.P2PEnabled() {
		t.Fatal("p2p deve estar habilitado por padrão quando enabled omitido no yaml")
	}
}

func TestDefaultP2PEnabledOnFreshInstall(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	loaded, err := loadAtPath(p)
	if err != nil {
		t.Fatalf("loadAtPath: %v", err)
	}
	if !loaded.P2PEnabled() {
		t.Fatal("instalação nova deve ter P2P habilitado")
	}
	if len(loaded.P2P.SignalingURLs) != 1 {
		t.Fatalf("signaling urls: %v", loaded.P2P.SignalingURLs)
	}
}
