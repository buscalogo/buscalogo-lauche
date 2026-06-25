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
