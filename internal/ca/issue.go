package ca

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	blcrypto "buscalogo-agent/internal/crypto"
	"buscalogo-agent/internal/ledger"
)

const IssueSkew = 5 * time.Minute

// IssueRequest is the JSON body for POST /api/ca/issue.
type IssueRequest struct {
	Domains     []string `json:"domains"`
	CSR         string   `json:"csr"`          // PEM
	OwnerPubKey string   `json:"owner_pubkey"` // hex Ed25519
	Timestamp   int64    `json:"timestamp"`    // unix ms
	Signature   string   `json:"signature"`    // hex
}

// IssueResponse is returned by the registry.
type IssueResponse struct {
	Cert  string `json:"cert"`  // leaf PEM
	Chain string `json:"chain"` // leaf + root PEM
}

// CanonicalIssuePayload builds the bytes signed by the domain owner.
func CanonicalIssuePayload(ownerPub []byte, domains []string, csrPEM []byte, timestampMS int64) []byte {
	norm := make([]string, 0, len(domains))
	for _, d := range domains {
		d = ledger.NormalizeDomain(d)
		if d != "" {
			norm = append(norm, d)
		}
	}
	sort.Strings(norm)
	sum := sha256.Sum256(csrPEM)
	var b bytes.Buffer
	writeLenBytes(&b, ownerPub)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(norm)))
	for _, d := range norm {
		writeLenBytes(&b, []byte(d))
	}
	writeLenBytes(&b, sum[:])
	_ = binary.Write(&b, binary.BigEndian, timestampMS)
	return b.Bytes()
}

func writeLenBytes(b *bytes.Buffer, v []byte) {
	_ = binary.Write(b, binary.BigEndian, uint32(len(v)))
	b.Write(v)
}

// SignIssueWithKey signs using ed25519 private key bytes.
func SignIssueWithKey(priv []byte, ownerPub []byte, domains []string, csrPEM []byte, timestampMS int64) ([]byte, error) {
	payload := CanonicalIssuePayload(ownerPub, domains, csrPEM, timestampMS)
	return blcrypto.Sign(priv, payload)
}

// VerifyIssueRequest checks signature and timestamp window.
func VerifyIssueRequest(ownerPub []byte, domains []string, csrPEM []byte, timestampMS int64, sig []byte) error {
	if len(ownerPub) != 32 {
		return fmt.Errorf("owner_pubkey inválida")
	}
	now := time.Now().UnixMilli()
	if timestampMS < now-IssueSkew.Milliseconds() || timestampMS > now+IssueSkew.Milliseconds() {
		return fmt.Errorf("timestamp fora da janela (±%s)", IssueSkew)
	}
	payload := CanonicalIssuePayload(ownerPub, domains, csrPEM, timestampMS)
	if !blcrypto.Verify(ownerPub, payload, sig) {
		return fmt.Errorf("assinatura inválida")
	}
	return nil
}

// GenerateLeafKeyAndCSR creates an ECDSA key + CSR for dnsNames.
func GenerateLeafKeyAndCSR(dnsNames []string) (keyPEM, csrPEM []byte, err error) {
	if len(dnsNames) == 0 {
		return nil, nil, fmt.Errorf("dnsNames vazio")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{
			Organization: []string{"BuscaLogo"},
			CommonName:   dnsNames[0],
		},
		DNSNames: dnsNames,
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, nil, err
	}
	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return keyPEM, csrPEM, nil
}

// WriteLeafMaterial writes site.key, site.crt and site-chain.crt into certDir.
func WriteLeafMaterial(certDir string, keyPEM, certPEM, chainPEM []byte) error {
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, LeafKeyName), keyPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, LeafCertName), certPEM, 0o644); err != nil {
		return err
	}
	if len(chainPEM) == 0 {
		chainPEM = certPEM
	}
	return os.WriteFile(filepath.Join(certDir, ChainName), chainPEM, 0o644)
}

// NormalizeDomains cleans and validates .bl/.lo hosts.
func NormalizeDomains(domains []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, d := range domains {
		d = ledger.NormalizeDomain(d)
		if d == "" || seen[d] {
			continue
		}
		if !ledger.ValidDomain(d) {
			return nil, fmt.Errorf("domínio inválido: %s", d)
		}
		seen[d] = true
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nenhum domínio válido")
	}
	sort.Strings(out)
	return out, nil
}

// DecodeHex soft-decodes hex strings.
func DecodeHex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	return hex.DecodeString(s)
}
