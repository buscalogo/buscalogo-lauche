package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v4"
)

type badgerStore struct {
	db *badger.DB
}

// OpenBadger abre (ou cria) o registro em path.
func OpenBadger(path string) (Store, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	opts := badger.DefaultOptions(path).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}
	return &badgerStore{db: db}, nil
}

func (s *badgerStore) Close() error { return s.db.Close() }

func (s *badgerStore) PutEvent(domain string, nonce uint64, raw []byte) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(eventKey(domain, nonce)), raw)
	})
}

func (s *badgerStore) GetEvent(domain string, nonce uint64) ([]byte, error) {
	var out []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(eventKey(domain, nonce)))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(v []byte) error {
			out = append([]byte(nil), v...)
			return nil
		})
	})
	return out, err
}

func (s *badgerStore) HasEventHash(hash []byte) (bool, error) {
	hex := fmt.Sprintf("%x", hash)
	found := false
	err := s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(hashKey(hex)))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	return found, err
}

func (s *badgerStore) PutEventHash(hash []byte) error {
	hex := fmt.Sprintf("%x", hash)
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(hashKey(hex)), []byte{1})
	})
}

func (s *badgerStore) PutRejected(hash []byte, reason string) error {
	hex := fmt.Sprintf("%x", hash)
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(rejectedKey(hex)), []byte(reason))
	})
}

func (s *badgerStore) GetDNS(domain string) (*DNSRecord, error) {
	var rec *DNSRecord
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(dnsKey(domain)))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(v []byte) error {
			var r DNSRecord
			if err := json.Unmarshal(v, &r); err != nil {
				return err
			}
			rec = &r
			return nil
		})
	})
	return rec, err
}

func (s *badgerStore) PutDNS(domain string, rec *DNSRecord) error {
	if rec == nil {
		return fmt.Errorf("dns record nil")
	}
	rec.Domain = domain
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(dnsKey(domain)), raw)
	})
}

func (s *badgerStore) DeleteDNS(domain string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(dnsKey(domain)))
	})
}

func (s *badgerStore) ListDNS() ([]*DNSRecord, error) {
	var out []*DNSRecord
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte("dns/")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var r DNSRecord
				if err := json.Unmarshal(v, &r); err != nil {
					return err
				}
				out = append(out, &r)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func (s *badgerStore) GetState(domain string) (*DomainState, error) {
	var st *DomainState
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(stateKey(domain)))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(v []byte) error {
			var x DomainState
			if err := json.Unmarshal(v, &x); err != nil {
				return err
			}
			st = &x
			return nil
		})
	})
	return st, err
}

func (s *badgerStore) PutState(domain string, st *DomainState) error {
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
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(stateKey(domain)), raw)
	})
}

func (s *badgerStore) DeleteState(domain string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(stateKey(domain)))
	})
}

func (s *badgerStore) ListAllEvents() ([][]byte, error) {
	var out [][]byte
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte("events/")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				out = append(out, append([]byte(nil), v...))
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func (s *badgerStore) WriteHostsFile() (string, error) {
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
