package registry

// Registry é a interface do registro distribuído de domínios .bl.
//
// Esboço de design (Fase 2):
//
// 1. Registros .bl são CRDTs (Conflict-free Replicated Data Types) de modo
//    last-writer-wins por domínio. Cada registro é assinado por uma chave
//    ed25519 do proprietário.
//
// 2. O agente mantém uma tabela local (leveldb/badger em data/registry/)
//    com: chave do domínio, registro mais recente, TTL, assinatura.
//
// 3. Propagação P2P: usa o Yggdrasil como transporte confiável entre nós.
//    Um protocolo simples sobre TCP/UNIX do admin socket ou uma porta
//    própria do agente faz push/pull de domínios conhecidos.
//
// 4. CoreDNS plugin: quando a query termina em .bl, consulta o Registry
//    local; se não encontrar, pergunta a peers conectados (DHT simples ou
//    gossip). A resposta pode ser AAAA (IPv6 Yggdrasil), A (IPv4 tunneled)
//    ou registros TXT/CNAME.
//
// 5. API do agente: POST /api/registry/{domain} para registrar/atualizar
//    um domínio; GET /api/registry/{domain} para consultar local.
//
// 6. Anti-spam: domínios .bl têm custo simbólico de proof-of-work ou
//    são rate-limited por chave pública; TTL expira se o nó dono ficar offline.
//
// Esta interface define o contrato mínimo para integração com CoreDNS e API.

type Record struct {
	Domain    string            `json:"domain"`
	OwnerKey  string            `json:"owner_key"`
	Addresses []string          `json:"addresses"`
	TTL       int               `json:"ttl"`
	Signature []byte            `json:"signature"`
	Meta      map[string]string `json:"meta,omitempty"`
}

type Registry interface {
	// Lookup retorna o registro mais recente de um domínio .bl.
	Lookup(domain string) (*Record, error)

	// Register publica/atualiza um registro localmente e propaga para peers.
	Register(rec *Record) error

	// Resolve encontra registros de forma distribuída (local + peers).
	Resolve(domain string) (*Record, error)
}

// Placeholder é uma implementação vazia usada enquanto o protocolo não existe.
type Placeholder struct{}

func (Placeholder) Lookup(domain string) (*Record, error) { return nil, nil }
func (Placeholder) Register(rec *Record) error            { return nil }
func (Placeholder) Resolve(domain string) (*Record, error) { return nil, nil }
