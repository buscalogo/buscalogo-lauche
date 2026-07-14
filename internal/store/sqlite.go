package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteStore struct {
	db *sql.DB
}

// OpenSQLite abre (ou cria) o registro SQLite em path.
func OpenSQLite(path string) (Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	schema := `
CREATE TABLE IF NOT EXISTS events (
  domain TEXT NOT NULL,
  nonce INTEGER NOT NULL,
  raw BLOB NOT NULL,
  PRIMARY KEY(domain, nonce)
);
CREATE TABLE IF NOT EXISTS state (
  domain TEXT PRIMARY KEY,
  raw BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS dns (
  domain TEXT PRIMARY KEY,
  raw BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS hashes (
  hash TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS rejected (
  hash TEXT PRIMARY KEY,
  reason TEXT NOT NULL
);`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }

func (s *sqliteStore) PutEvent(domain string, nonce uint64, raw []byte) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO events(domain, nonce, raw) VALUES(?,?,?)`, domain, nonce, raw)
	return err
}

func (s *sqliteStore) GetEvent(domain string, nonce uint64) ([]byte, error) {
	var raw []byte
	err := s.db.QueryRow(`SELECT raw FROM events WHERE domain=? AND nonce=?`, domain, nonce).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return raw, err
}

func (s *sqliteStore) HasEventHash(hash []byte) (bool, error) {
	hex := fmt.Sprintf("%x", hash)
	var x string
	err := s.db.QueryRow(`SELECT hash FROM hashes WHERE hash=?`, hex).Scan(&x)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *sqliteStore) PutEventHash(hash []byte) error {
	hex := fmt.Sprintf("%x", hash)
	_, err := s.db.Exec(`INSERT OR IGNORE INTO hashes(hash) VALUES(?)`, hex)
	return err
}

func (s *sqliteStore) PutRejected(hash []byte, reason string) error {
	hex := fmt.Sprintf("%x", hash)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO rejected(hash, reason) VALUES(?,?)`, hex, reason)
	return err
}

func (s *sqliteStore) GetDNS(domain string) (*DNSRecord, error) {
	var raw []byte
	err := s.db.QueryRow(`SELECT raw FROM dns WHERE domain=?`, domain).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r DNSRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *sqliteStore) PutDNS(domain string, rec *DNSRecord) error {
	if rec == nil {
		return fmt.Errorf("dns record nil")
	}
	rec.Domain = domain
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO dns(domain, raw) VALUES(?,?)`, domain, raw)
	return err
}

func (s *sqliteStore) DeleteDNS(domain string) error {
	_, err := s.db.Exec(`DELETE FROM dns WHERE domain=?`, domain)
	return err
}

func (s *sqliteStore) ListDNS() ([]*DNSRecord, error) {
	rows, err := s.db.Query(`SELECT raw FROM dns ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DNSRecord
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r DNSRecord
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *sqliteStore) GetState(domain string) (*DomainState, error) {
	var raw []byte
	err := s.db.QueryRow(`SELECT raw FROM state WHERE domain=?`, domain).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st DomainState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *sqliteStore) PutState(domain string, st *DomainState) error {
	if st == nil {
		return fmt.Errorf("state nil")
	}
	st.Domain = domain
	if st.UpdatedAt == 0 {
		st.UpdatedAt = time.Now().UnixMilli()
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO state(domain, raw) VALUES(?,?)`, domain, raw)
	return err
}

func (s *sqliteStore) DeleteState(domain string) error {
	_, err := s.db.Exec(`DELETE FROM state WHERE domain=?`, domain)
	return err
}

func (s *sqliteStore) ListAllEvents() ([][]byte, error) {
	rows, err := s.db.Query(`SELECT raw FROM events ORDER BY domain, nonce`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, append([]byte(nil), raw...))
	}
	return out, rows.Err()
}

func (s *sqliteStore) WriteHostsFile() (string, error) {
	path, err := HostsFilePath()
	if err != nil {
		return "", err
	}
	recs, err := s.ListDNS()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# BuscaLogo registry hosts — gerado automaticamente\n")
	for _, r := range recs {
		if r == nil || r.Domain == "" {
			continue
		}
		addrs := append([]string{}, r.AAAA...)
		addrs = append(addrs, r.A...)
		for _, a := range addrs {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			fmt.Fprintf(&b, "%s %s\n", a, r.Domain)
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
