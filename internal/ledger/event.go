package ledger

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	blcrypto "buscalogo-agent/internal/crypto"
)

const (
	TypeRegister byte = 1
	TypeTransfer byte = 2
	TypeUpdate   byte = 3

	MaxClockSkewMs = 5 * 60 * 1000
	DefaultTTL     = 300
)

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.bl$`)

// Records são os RRsets publicados.
type Records struct {
	AAAA []string `json:"aaaa,omitempty"`
	A    []string `json:"a,omitempty"`
	TXT  []string `json:"txt,omitempty"`
	TTL  int      `json:"ttl,omitempty"`
}

// DomainEvent espelha o contrato Protobuf (serializado em JSON na rede v1).
type DomainEvent struct {
	Type          byte    `json:"type"`
	Domain        string  `json:"domain"`
	OwnerPubkey   []byte  `json:"owner_pubkey"`
	TargetPubkey  []byte  `json:"target_pubkey,omitempty"`
	Records       Records `json:"records"`
	Nonce         uint64  `json:"nonce"`
	Timestamp     int64   `json:"timestamp"`
	Signature     []byte  `json:"signature"`
}

func TypeName(t byte) string {
	switch t {
	case TypeRegister:
		return "REGISTER"
	case TypeTransfer:
		return "TRANSFER"
	case TypeUpdate:
		return "UPDATE"
	default:
		return "UNKNOWN"
	}
}

func NormalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimSpace(domain))
}

func ValidDomain(domain string) bool {
	return domainRe.MatchString(NormalizeDomain(domain))
}

func (e *DomainEvent) CanonicalPayload() []byte {
	ttl := e.Records.TTL
	_ = ttl
	return blcrypto.SignBytes(
		e.Type,
		e.Domain,
		e.OwnerPubkey,
		e.TargetPubkey,
		e.Records.AAAA,
		e.Records.A,
		e.Records.TXT,
		e.Nonce,
		e.Timestamp,
	)
}

func (e *DomainEvent) Sign(priv ed25519.PrivateKey) error {
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("chave privada inválida")
	}
	pub := priv.Public().(ed25519.PublicKey)
	e.OwnerPubkey = append([]byte(nil), pub...)
	e.Domain = NormalizeDomain(e.Domain)
	if e.Timestamp == 0 {
		e.Timestamp = time.Now().UnixMilli()
	}
	sig, err := blcrypto.Sign(priv, e.CanonicalPayload())
	if err != nil {
		return err
	}
	e.Signature = sig
	return nil
}

func (e *DomainEvent) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

func UnmarshalEvent(raw []byte) (*DomainEvent, error) {
	var e DomainEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	e.Domain = NormalizeDomain(e.Domain)
	return &e, nil
}

func (e *DomainEvent) Hash() []byte {
	raw, err := e.Marshal()
	if err != nil {
		return blcrypto.Hash(e.CanonicalPayload())
	}
	// Hash do evento assinado completo (estável para message id).
	return blcrypto.Hash(raw)
}

func (e *DomainEvent) VerifySignature() bool {
	return blcrypto.Verify(e.OwnerPubkey, e.CanonicalPayload(), e.Signature)
}

func ValidateBasic(e *DomainEvent, nowMs int64) error {
	return validateBasic(e, nowMs, true)
}

// ValidateHistorical valida assinatura/schema sem exigir skew de clock (catch-up sync).
func ValidateHistorical(e *DomainEvent) error {
	return validateBasic(e, time.Now().UnixMilli(), false)
}

func validateBasic(e *DomainEvent, nowMs int64, checkSkew bool) error {
	if e == nil {
		return fmt.Errorf("evento nil")
	}
	e.Domain = NormalizeDomain(e.Domain)
	if !ValidDomain(e.Domain) {
		return fmt.Errorf("domínio inválido: %s", e.Domain)
	}
	if e.Type != TypeRegister && e.Type != TypeTransfer && e.Type != TypeUpdate {
		return fmt.Errorf("tipo inválido: %d", e.Type)
	}
	if len(e.OwnerPubkey) != ed25519.PublicKeySize {
		return fmt.Errorf("owner_pubkey inválida")
	}
	if e.Type == TypeTransfer && len(e.TargetPubkey) != ed25519.PublicKeySize {
		return fmt.Errorf("target_pubkey inválida")
	}
	if checkSkew {
		if nowMs == 0 {
			nowMs = time.Now().UnixMilli()
		}
		skew := e.Timestamp - nowMs
		if skew < 0 {
			skew = -skew
		}
		if skew > MaxClockSkewMs {
			return fmt.Errorf("timestamp fora do skew permitido")
		}
	}
	if !e.VerifySignature() {
		return fmt.Errorf("assinatura inválida")
	}
	return nil
}

// WinnerREGISTER compara dois REGISTER válidos: menor (timestamp, hash) vence.
func WinnerREGISTER(aTS int64, aHash []byte, bTS int64, bHash []byte) int {
	if aTS < bTS {
		return -1
	}
	if aTS > bTS {
		return 1
	}
	return bytes.Compare(aHash, bHash)
}
