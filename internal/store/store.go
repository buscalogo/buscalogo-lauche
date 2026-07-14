package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"buscalogo-agent/internal/paths"
)

// DNSRecord é o snapshot materializado para resolução rápida.
type DNSRecord struct {
	Domain string   `json:"domain"`
	Owner  []byte   `json:"owner"`
	AAAA   []string `json:"aaaa,omitempty"`
	A      []string `json:"a,omitempty"`
	TXT    []string `json:"txt,omitempty"`
	TTL    int      `json:"ttl"`
	Nonce  uint64   `json:"nonce"`
}

// DomainState é o estado de propriedade do domínio.
type DomainState struct {
	Domain    string `json:"domain"`
	Owner     []byte `json:"owner"`
	Nonce     uint64 `json:"nonce"`
	UpdatedAt int64  `json:"updated_at"`
	// RegisterHash / RegisterTS usados em First-Valid-Wins e rollback.
	RegisterHash []byte `json:"register_hash,omitempty"`
	RegisterTS   int64  `json:"register_ts,omitempty"`
}

// Store é a persistência local do ledger .bl (writer: ledger.Engine).
type Store interface {
	PutEvent(domain string, nonce uint64, raw []byte) error
	GetEvent(domain string, nonce uint64) ([]byte, error)
	HasEventHash(hash []byte) (bool, error)
	PutEventHash(hash []byte) error
	PutRejected(hash []byte, reason string) error

	GetDNS(domain string) (*DNSRecord, error)
	PutDNS(domain string, rec *DNSRecord) error
	DeleteDNS(domain string) error
	ListDNS() ([]*DNSRecord, error)

	GetState(domain string) (*DomainState, error)
	PutState(domain string, st *DomainState) error
	DeleteState(domain string) error

	// ListAllEvents devolve todos os eventos cruos (catch-up sync).
	ListAllEvents() ([][]byte, error)

	// WriteHostsFile materializa data/registry/hosts no formato hosts(5).
	WriteHostsFile() (string, error)

	Close() error
}

// Open abre Badger (default) ou SQLite conforme engine.
func Open(engine, path string) (Store, error) {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		engine = "badger"
	}
	if path == "" {
		data, err := paths.Data()
		if err != nil {
			return nil, err
		}
		if engine == "sqlite" {
			path = filepath.Join(data, "registry.db")
		} else {
			path = filepath.Join(data, "registry")
		}
	}
	switch engine {
	case "badger":
		return OpenBadger(path)
	case "sqlite":
		return OpenSQLite(path)
	default:
		return nil, fmt.Errorf("registry engine desconhecido: %s (use badger ou sqlite)", engine)
	}
}

// HostsFilePath retorna o caminho do hosts materializado do registry.
func HostsFilePath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "registry")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "hosts"), nil
}
