package registry

// Registry é a interface do registro distribuído de domínios .bl.
// Implementação viva: internal/ledger.Engine (append-only + GossipSub).

type Record struct {
	Domain    string            `json:"domain"`
	OwnerKey  string            `json:"owner_key"`
	Addresses []string          `json:"addresses"`
	TTL       int               `json:"ttl"`
	Signature []byte            `json:"signature,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

type Registry interface {
	Lookup(domain string) (*Record, error)
	Register(rec *Record) error
	Resolve(domain string) (*Record, error)
}

// Placeholder é uma implementação vazia (testes / registry disabled).
type Placeholder struct{}

func (Placeholder) Lookup(domain string) (*Record, error) { return nil, nil }
func (Placeholder) Register(rec *Record) error            { return nil }
func (Placeholder) Resolve(domain string) (*Record, error) { return nil, nil }
