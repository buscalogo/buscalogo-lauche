package couchdb

import (
	"strings"
	"testing"
)

func TestRenderLocalINI_NoAuth(t *testing.T) {
	ini := renderLocalINI(renderOpts{
		DatabaseDir: "/data/databases",
		IndexDir:    "/data/indexes",
		LogFile:     "/data/logs/couchdb.log",
		UUID:        "test-uuid",
		Bind:        "127.0.0.1",
		Port:        5984,
		AdminUser:   "buscalogo",
		AdminPass:   "generated-secret",
	})
	if !strings.Contains(ini, "database_dir = /data/databases") {
		t.Fatalf("database_dir ausente: %s", ini)
	}
	if !strings.Contains(ini, "require_valid_user = false") {
		t.Fatalf("auth local esperada: %s", ini)
	}
	if !strings.Contains(ini, "[admins]") {
		t.Fatalf("admins obrigatório no CouchDB 3.x: %s", ini)
	}
	if !strings.Contains(ini, "single_node = true") {
		t.Fatalf("single_node ausente: %s", ini)
	}
	if !strings.Contains(ini, "file = /data/logs/couchdb.log") {
		t.Fatalf("log file ausente: %s", ini)
	}
}

func TestRenderLocalINI_WithAuth(t *testing.T) {
	ini := renderLocalINI(renderOpts{
		DatabaseDir: "/data/databases",
		IndexDir:    "/data/indexes",
		LogFile:     "/data/logs/couchdb.log",
		UUID:        "test-uuid",
		Bind:        "127.0.0.1",
		Port:        5984,
		AdminUser:   "admin",
		AdminPass:   "secret",
	})
	if !strings.Contains(ini, "require_valid_user = false") {
		t.Fatalf("localhost sem auth obrigatória: %s", ini)
	}
	if !strings.Contains(ini, "admin = secret") {
		t.Fatalf("admin ausente: %s", ini)
	}
}
