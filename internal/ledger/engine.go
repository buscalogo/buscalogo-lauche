package ledger

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"sort"
	"sync"
	"time"

	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/registry"
	"buscalogo-agent/internal/store"
)

// Publisher envia eventos para a rede (GossipSub). Opcional.
type Publisher interface {
	Publish(raw []byte) error
}

// Engine aplica DomainEvents no Store local e publica gossip.
type Engine struct {
	store store.Store
	buf   *logx.Buffer
	pub   Publisher

	mu           sync.Mutex
	onHostsWrite func(path string)
	rate         map[string][]int64 // pubkey hex → REGISTER timestamps
}

func NewEngine(st store.Store, buf *logx.Buffer) *Engine {
	return &Engine{
		store: st,
		buf:   buf,
		rate:  make(map[string][]int64),
	}
}

func (e *Engine) SetPublisher(p Publisher) { e.pub = p }

func (e *Engine) SetOnHostsWrite(fn func(path string)) { e.onHostsWrite = fn }

func (e *Engine) Store() store.Store { return e.store }

// Apply valida e aplica um evento; retorna se mudou estado DNS.
func (e *Engine) Apply(ev *DomainEvent) (applied bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.applyLocked(ev, true)
}

// Ingest aplica evento recebido via gossip (não re-publica).
func (e *Engine) Ingest(raw []byte) error {
	ev, err := UnmarshalEvent(raw)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err = e.applyLocked(ev, false)
	return err
}

// IngestHistorical aplica evento antigo (sync catch-up) sem exigir skew de clock.
func (e *Engine) IngestHistorical(raw []byte) error {
	ev, err := UnmarshalEvent(raw)
	if err != nil {
		return err
	}
	if err := ValidateHistorical(ev); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.applyHistoricalLocked(ev)
}

// IngestHistoricalBatch aplica um lote em multipass (REGISTER → UPDATE por nonce),
// sem rate-limit — essencial no catch-up com vários domínios.
func (e *Engine) IngestHistoricalBatch(raws [][]byte) (applied, skipped, failed int, sampleErrs []string) {
	type item struct {
		ev  *DomainEvent
		raw []byte
	}
	var items []item
	for _, raw := range raws {
		ev, err := UnmarshalEvent(raw)
		if err != nil {
			failed++
			if len(sampleErrs) < 8 {
				sampleErrs = append(sampleErrs, fmt.Sprintf("unmarshal: %v", err))
			}
			continue
		}
		if err := ValidateHistorical(ev); err != nil {
			failed++
			if len(sampleErrs) < 8 {
				sampleErrs = append(sampleErrs, fmt.Sprintf("%s: %v", ev.Domain, err))
			}
			continue
		}
		items = append(items, item{ev: ev, raw: raw})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ev.Domain != items[j].ev.Domain {
			return items[i].ev.Domain < items[j].ev.Domain
		}
		if items[i].ev.Nonce != items[j].ev.Nonce {
			return items[i].ev.Nonce < items[j].ev.Nonce
		}
		// REGISTER antes de UPDATE/TRANSFER no mesmo nonce (não deveria haver).
		return items[i].ev.Type < items[j].ev.Type
	})

	pending := items
	for pass := 0; pass < 8 && len(pending) > 0; pass++ {
		var next []item
		progress := false
		e.mu.Lock()
		for _, it := range pending {
			h := it.ev.Hash()
			if ok, _ := e.store.HasEventHash(h); ok {
				skipped++
				continue
			}
			err := e.applyHistoricalLocked(it.ev)
			if err != nil {
				next = append(next, it)
				continue
			}
			// applyHistoricalLocked já faz dedup interno; se hash ainda não marcado, conta
			if ok, _ := e.store.HasEventHash(h); ok {
				applied++
				progress = true
			} else {
				// rejeitado sem erro (ex.: first-valid-wins loser) — marca skip
				skipped++
			}
		}
		e.mu.Unlock()
		if !progress {
			for _, it := range next {
				failed++
				if len(sampleErrs) < 8 {
					e.mu.Lock()
					err := e.applyHistoricalLocked(it.ev)
					e.mu.Unlock()
					msg := "não aplicado"
					if err != nil {
						msg = err.Error()
					}
					sampleErrs = append(sampleErrs, fmt.Sprintf("%s nonce=%d: %s", it.ev.Domain, it.ev.Nonce, msg))
				}
			}
			break
		}
		pending = next
	}
	return applied, skipped, failed, sampleErrs
}

func (e *Engine) applyHistoricalLocked(ev *DomainEvent) error {
	h := ev.Hash()
	if ok, _ := e.store.HasEventHash(h); ok {
		return nil
	}
	st, err := e.store.GetState(ev.Domain)
	if err != nil {
		return err
	}
	switch ev.Type {
	case TypeRegister:
		applied, err := e.applyRegister(ev, st, h, true)
		if err != nil {
			return err
		}
		if !applied {
			return nil
		}
	case TypeUpdate:
		if err := e.applyUpdate(ev, st); err != nil {
			_ = e.store.PutRejected(h, err.Error())
			return err
		}
	case TypeTransfer:
		if err := e.applyTransfer(ev, st); err != nil {
			_ = e.store.PutRejected(h, err.Error())
			return err
		}
	default:
		return fmt.Errorf("tipo não suportado")
	}
	raw, err := ev.Marshal()
	if err != nil {
		return err
	}
	if err := e.store.PutEvent(ev.Domain, ev.Nonce, raw); err != nil {
		return err
	}
	_ = e.store.PutEventHash(h)
	if path, err := e.store.WriteHostsFile(); err == nil && e.onHostsWrite != nil {
		e.onHostsWrite(path)
	}
	return nil
}

func (e *Engine) ExportAllEvents() ([][]byte, error) {
	return e.store.ListAllEvents()
}

func (e *Engine) applyLocked(ev *DomainEvent, publish bool) (bool, error) {
	if err := ValidateBasic(ev, time.Now().UnixMilli()); err != nil {
		return false, err
	}
	h := ev.Hash()
	if ok, _ := e.store.HasEventHash(h); ok {
		return false, nil // dedup
	}

	st, err := e.store.GetState(ev.Domain)
	if err != nil {
		return false, err
	}

	switch ev.Type {
	case TypeRegister:
		applied, err := e.applyRegister(ev, st, h, false)
		if err != nil || !applied {
			return applied, err
		}
	case TypeUpdate:
		if err := e.applyUpdate(ev, st); err != nil {
			_ = e.store.PutRejected(h, err.Error())
			return false, err
		}
	case TypeTransfer:
		if err := e.applyTransfer(ev, st); err != nil {
			_ = e.store.PutRejected(h, err.Error())
			return false, err
		}
	default:
		return false, fmt.Errorf("tipo não suportado")
	}

	raw, err := ev.Marshal()
	if err != nil {
		return false, err
	}
	if err := e.store.PutEvent(ev.Domain, ev.Nonce, raw); err != nil {
		return false, err
	}
	_ = e.store.PutEventHash(h)
	if path, err := e.store.WriteHostsFile(); err == nil && e.onHostsWrite != nil {
		e.onHostsWrite(path)
	}
	if publish && e.pub != nil {
		if err := e.pub.Publish(raw); err != nil && e.buf != nil {
			e.buf.Warnf("ledger", "publish gossip: %v", err)
		}
	}
	return true, nil
}

func (e *Engine) applyRegister(ev *DomainEvent, st *store.DomainState, h []byte, skipRate bool) (bool, error) {
	if !skipRate && !e.allowRegisterRate(ev.OwnerPubkey, ev.Timestamp) {
		_ = e.store.PutRejected(h, "rate limit REGISTER")
		return false, fmt.Errorf("rate limit REGISTER (máx 5/hora por chave)")
	}
	if st == nil {
		if err := e.writeOwnership(ev, h, ev.OwnerPubkey); err != nil {
			return false, err
		}
		return true, nil
	}
	// Conflito First-Valid-Wins: menor (ts, hash) vence.
	cmp := WinnerREGISTER(ev.Timestamp, h, st.RegisterTS, st.RegisterHash)
	if len(st.RegisterHash) == 0 {
		// Estado sem metadados de register — trata como já ocupado.
		_ = e.store.PutRejected(h, "domain already registered")
		return false, fmt.Errorf("domínio já registrado")
	}
	if cmp >= 0 {
		// Existente vence ou empate (mesmo evento).
		if bytes.Equal(h, st.RegisterHash) {
			return false, nil
		}
		_ = e.store.PutRejected(h, "first-valid-wins loser")
		return false, fmt.Errorf("domínio já registrado (First-Valid-Wins)")
	}
	// Incoming vence: rollback + apply.
	if e.buf != nil {
		e.buf.Warnf("ledger", "rollback REGISTER %s — novo vencedor First-Valid-Wins", ev.Domain)
	}
	_ = e.store.PutRejected(st.RegisterHash, "superseded by earlier REGISTER")
	if err := e.writeOwnership(ev, h, ev.OwnerPubkey); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) applyUpdate(ev *DomainEvent, st *store.DomainState) error {
	if st == nil {
		return fmt.Errorf("domínio não registrado")
	}
	if !bytes.Equal(ev.OwnerPubkey, st.Owner) {
		return fmt.Errorf("não é o dono atual")
	}
	if ev.Nonce != st.Nonce+1 {
		return fmt.Errorf("nonce inválido: want %d got %d", st.Nonce+1, ev.Nonce)
	}
	return e.writeOwnership(ev, st.RegisterHash, st.Owner)
}

func (e *Engine) applyTransfer(ev *DomainEvent, st *store.DomainState) error {
	if st == nil {
		return fmt.Errorf("domínio não registrado")
	}
	if !bytes.Equal(ev.OwnerPubkey, st.Owner) {
		return fmt.Errorf("não é o dono atual")
	}
	if ev.Nonce != st.Nonce+1 {
		return fmt.Errorf("nonce inválido: want %d got %d", st.Nonce+1, ev.Nonce)
	}
	if len(ev.TargetPubkey) != ed25519.PublicKeySize {
		return fmt.Errorf("target inválido")
	}
	// Após transfer, dono = target; records podem vir vazios (mantém DNS).
	newOwner := append([]byte(nil), ev.TargetPubkey...)
	dns, _ := e.store.GetDNS(ev.Domain)
	aaaa, a, txt := ev.Records.AAAA, ev.Records.A, ev.Records.TXT
	ttl := ev.Records.TTL
	if dns != nil {
		if len(aaaa) == 0 {
			aaaa = dns.AAAA
		}
		if len(a) == 0 {
			a = dns.A
		}
		if len(txt) == 0 {
			txt = dns.TXT
		}
		if ttl == 0 {
			ttl = dns.TTL
		}
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	st2 := &store.DomainState{
		Domain:       ev.Domain,
		Owner:        newOwner,
		Nonce:        ev.Nonce,
		UpdatedAt:    ev.Timestamp,
		RegisterHash: st.RegisterHash,
		RegisterTS:   st.RegisterTS,
	}
	if err := e.store.PutState(ev.Domain, st2); err != nil {
		return err
	}
	return e.store.PutDNS(ev.Domain, &store.DNSRecord{
		Domain: ev.Domain,
		Owner:  newOwner,
		AAAA:   aaaa,
		A:      a,
		TXT:    txt,
		TTL:    ttl,
		Nonce:  ev.Nonce,
	})
}

func (e *Engine) writeOwnership(ev *DomainEvent, regHash, owner []byte) error {
	ttl := ev.Records.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	regTS := ev.Timestamp
	if ev.Type != TypeRegister {
		if st, _ := e.store.GetState(ev.Domain); st != nil {
			regTS = st.RegisterTS
			if len(regHash) == 0 {
				regHash = st.RegisterHash
			}
		}
	}
	st := &store.DomainState{
		Domain:       ev.Domain,
		Owner:        append([]byte(nil), owner...),
		Nonce:        ev.Nonce,
		UpdatedAt:    ev.Timestamp,
		RegisterHash: append([]byte(nil), regHash...),
		RegisterTS:   regTS,
	}
	if err := e.store.PutState(ev.Domain, st); err != nil {
		return err
	}
	return e.store.PutDNS(ev.Domain, &store.DNSRecord{
		Domain: ev.Domain,
		Owner:  owner,
		AAAA:   ev.Records.AAAA,
		A:      ev.Records.A,
		TXT:    ev.Records.TXT,
		TTL:    ttl,
		Nonce:  ev.Nonce,
	})
}

func (e *Engine) allowRegisterRate(pub []byte, ts int64) bool {
	key := fmt.Sprintf("%x", pub)
	window := ts - 3600*1000
	var kept []int64
	for _, t := range e.rate[key] {
		if t >= window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= 5 {
		e.rate[key] = kept
		return false
	}
	kept = append(kept, ts)
	e.rate[key] = kept
	return true
}

// SignAndApply cria evento assinado e aplica+publica.
func (e *Engine) SignAndApply(priv ed25519.PrivateKey, typ byte, domain string, records Records, target []byte) (*DomainEvent, error) {
	domain = NormalizeDomain(domain)
	e.mu.Lock()
	defer e.mu.Unlock()

	st, err := e.store.GetState(domain)
	if err != nil {
		return nil, err
	}
	var nonce uint64
	switch typ {
	case TypeRegister:
		nonce = 0
	case TypeUpdate, TypeTransfer:
		if st == nil {
			return nil, fmt.Errorf("domínio não registrado")
		}
		nonce = st.Nonce + 1
	default:
		return nil, fmt.Errorf("tipo inválido")
	}
	ev := &DomainEvent{
		Type:         typ,
		Domain:       domain,
		TargetPubkey: target,
		Records:      records,
		Nonce:        nonce,
		Timestamp:    time.Now().UnixMilli(),
	}
	if err := ev.Sign(priv); err != nil {
		return nil, err
	}
	if _, err := e.applyLocked(ev, true); err != nil {
		return nil, err
	}
	return ev, nil
}

// Lookup implementa registry.Registry.
func (e *Engine) Lookup(domain string) (*registry.Record, error) {
	domain = NormalizeDomain(domain)
	dns, err := e.store.GetDNS(domain)
	if err != nil || dns == nil {
		return nil, err
	}
	addrs := append([]string{}, dns.AAAA...)
	addrs = append(addrs, dns.A...)
	return &registry.Record{
		Domain:    domain,
		OwnerKey:  fmt.Sprintf("%x", dns.Owner),
		Addresses: addrs,
		TTL:       dns.TTL,
		Meta: map[string]string{
			"nonce": fmt.Sprintf("%d", dns.Nonce),
		},
	}, nil
}

func (e *Engine) Register(rec *registry.Record) error {
	return fmt.Errorf("use SignAndApply via API autenticada")
}

func (e *Engine) Resolve(domain string) (*registry.Record, error) {
	return e.Lookup(domain)
}
