package account

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegisterFileBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BUSCALOGO_HOME", home)
	// paths.Data() is under home/data for portable binary layout — use exe dir via BUSCALOGO_HOME
	// Ensure Data() resolves: BUSCALOGO_HOME is homeDir(); Data joins "data".
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	s := New(nil, nil)
	if s.StorageBackend() != "file" {
		t.Fatalf("storage=%s", s.StorageBackend())
	}
	if s.HasAccount() {
		t.Fatal("não deveria ter conta")
	}

	user, token, err := s.Register("akira", "secret12")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if user == nil || user.Username != "akira" || token == "" {
		t.Fatalf("user/token inválidos: %+v %q", user, token)
	}
	if !s.HasAccount() {
		t.Fatal("HasAccount=false após register")
	}
	sid, err := s.EnsureServerID()
	if err != nil || sid == "" {
		t.Fatalf("server_id: %v %q", err, sid)
	}

	s2 := New(nil, nil)
	u2, tok2, err := s2.Login("akira", "secret12")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if u2.ID != user.ID || tok2 == "" {
		t.Fatalf("login mismatch %+v", u2)
	}

	_, _, err = s2.Register("akira", "outro12")
	if err == nil {
		t.Fatal("esperava username em uso")
	}
}
