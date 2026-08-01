package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	RootCertName = "rootCA.pem"
	RootKeyName  = "rootCA-key.pem"
	LeafCertName = "site.crt"
	LeafKeyName  = "site.key"
	ChainName    = "site-chain.crt"

	DefaultLeafTTL = 90 * 24 * time.Hour
	DefaultRootTTL = 10 * 365 * 24 * time.Hour
)

// Authority is a BuscaLogo root CA (registry-side only).
type Authority struct {
	Dir  string
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	PEM  []byte // root certificate PEM
}

// DirForHome returns $HOME/data/certs/ca.
func DirForHome(home string) string {
	return filepath.Join(home, "data", "certs", "ca")
}

// EnsureRoot loads an existing root CA from dir.
// It does NOT create a new CA — use GenerateRoot or EnsureRootAllowGenerate for bootstrap.
func EnsureRoot(dir string) (*Authority, error) {
	certPath := filepath.Join(dir, RootCertName)
	keyPath := filepath.Join(dir, RootKeyName)
	if _, err := os.Stat(certPath); err != nil {
		return nil, fmt.Errorf("CA canônica ausente em %s — copie rootCA.pem + rootCA-key.pem desta malha (uma única raiz). Para bootstrap local: ca.generate_if_missing: true ou BUSCALOGO_CA_GENERATE=1", dir)
	}
	if _, err := os.Stat(keyPath); err != nil {
		return nil, fmt.Errorf("CA incompleta em %s (falta %s) — a chave deve ser a mesma em todos os registries", dir, RootKeyName)
	}
	return LoadRoot(dir)
}

// EnsureRootAllowGenerate loads the CA or creates one once (dev / first seed only).
func EnsureRootAllowGenerate(dir string) (*Authority, error) {
	certPath := filepath.Join(dir, RootCertName)
	keyPath := filepath.Join(dir, RootKeyName)
	if st, err := os.Stat(certPath); err == nil && !st.IsDir() {
		if _, err := os.Stat(keyPath); err == nil {
			return LoadRoot(dir)
		}
	}
	return GenerateRoot(dir)
}

// FingerprintSHA256 returns the hex SHA-256 of the root cert DER.
func (a *Authority) FingerprintSHA256() string {
	if a == nil || a.Cert == nil {
		return ""
	}
	sum := sha256.Sum256(a.Cert.Raw)
	return fmt.Sprintf("%x", sum[:])
}

// LoadRoot loads an existing root CA from dir (requires cert + private key).
func LoadRoot(dir string) (*Authority, error) {
	auth, err := LoadMeshCA(dir)
	if err != nil {
		return nil, err
	}
	if !auth.CanIssue() {
		return nil, fmt.Errorf("CA incompleta em %s (falta %s) — só registries emissores precisam da chave", dir, RootKeyName)
	}
	return auth, nil
}

// LoadMeshCA loads rootCA.pem and, se existir, rootCA-key.pem.
// Registries espelho podem ter só o PEM público (CanIssue=false).
func LoadMeshCA(dir string) (*Authority, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, RootCertName))
	if err != nil {
		return nil, fmt.Errorf("CA canônica ausente em %s — copie rootCA.pem desta malha", dir)
	}
	cert, err := parseCertPEM(certPEM)
	if err != nil {
		return nil, fmt.Errorf("rootCA.pem: %w", err)
	}
	auth := &Authority{Dir: dir, Cert: cert, PEM: certPEM}
	keyPEM, err := os.ReadFile(filepath.Join(dir, RootKeyName))
	if err != nil {
		if os.IsNotExist(err) {
			return auth, nil
		}
		return nil, fmt.Errorf("rootCA-key.pem: %w", err)
	}
	key, err := parseECKeyPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("rootCA-key.pem: %w", err)
	}
	auth.Key = key
	return auth, nil
}

// CanIssue reports whether this node holds the private key and can sign leaves.
func (a *Authority) CanIssue() bool {
	return a != nil && a.Cert != nil && a.Key != nil
}

// GenerateRoot creates a new ECDSA P-256 root CA.
func GenerateRoot(dir string) (*Authority, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization:       []string{"BuscaLogo"},
			OrganizationalUnit: []string{"Mesh CA"},
			CommonName:         "BuscaLogo Root CA",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(DefaultRootTTL),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
		MaxPathLenZero:        false,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	certPath := filepath.Join(dir, RootCertName)
	keyPath := filepath.Join(dir, RootKeyName)
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Authority{Dir: dir, Cert: cert, Key: key, PEM: certPEM}, nil
}

// SignCSR signs a CSR for the given DNS SANs (must be non-empty).
func (a *Authority) SignCSR(csrPEM []byte, dnsNames []string, ttl time.Duration) (certPEM, chainPEM []byte, err error) {
	if a == nil || a.Cert == nil || a.Key == nil {
		return nil, nil, fmt.Errorf("este nó não tem chave CA — não assina certificados")
	}
	if len(dnsNames) == 0 {
		return nil, nil, fmt.Errorf("nenhum DNS SAN")
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, nil, fmt.Errorf("CSR PEM inválido")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, nil, fmt.Errorf("assinatura CSR: %w", err)
	}
	if ttl <= 0 {
		ttl = DefaultLeafTTL
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"BuscaLogo"},
			CommonName:   dnsNames[0],
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(ttl),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.Cert, csr.PublicKey, a.Key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	chainPEM = append(append([]byte{}, certPEM...), a.PEM...)
	return certPEM, chainPEM, nil
}

// Status returns public CA metadata.
func (a *Authority) Status() map[string]any {
	if a == nil || a.Cert == nil {
		return map[string]any{
			"ready":          false,
			"can_issue":      false,
			"root_available": false,
		}
	}
	return map[string]any{
		"ready":              a.CanIssue(),
		"can_issue":          a.CanIssue(),
		"root_available":     true,
		"subject":            a.Cert.Subject.String(),
		"not_before":         a.Cert.NotBefore.UTC().Format(time.RFC3339),
		"not_after":          a.Cert.NotAfter.UTC().Format(time.RFC3339),
		"serial":             a.Cert.SerialNumber.String(),
		"fingerprint_sha256": a.FingerprintSHA256(),
	}
}

func parseCertPEM(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("PEM de certificado inválido")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseECKeyPEM(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("PEM de chave inválido")
	}
	if block.Type == "EC PRIVATE KEY" {
		return x509.ParseECPrivateKey(block.Bytes)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("chave não é ECDSA")
	}
	return ec, nil
}
