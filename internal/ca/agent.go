package ca

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
)

// IssueURLResolver returns candidate registry base URLs for CA issue/root.
type IssueURLResolver func() []string

// SigningKeyFunc returns the account Ed25519 private key.
type SigningKeyFunc func() (ed25519.PrivateKey, error)

// AgentHelper helps Agents fetch root CA and issue leaf certs from registries.
type AgentHelper struct {
	Cfg     *config.Config
	Buf     *logx.Buffer
	Resolve IssueURLResolver
	SignKey SigningKeyFunc
	// FilterDomains optionally restricts SANs to domains the account owns in the ledger.
	FilterDomains func(domains []string) []string

	mu   sync.RWMutex
	root []byte
}

// SetRootCache stores a root PEM in memory and data/certs/rootCA.pem.
func (h *AgentHelper) SetRootCache(pemBytes []byte) error {
	if len(pemBytes) == 0 {
		return fmt.Errorf("root vazio")
	}
	h.mu.Lock()
	h.root = append([]byte{}, pemBytes...)
	h.mu.Unlock()
	dir, err := paths.Certs()
	if err != nil {
		return err
	}
	return WriteRootFile(filepath.Join(dir, RootCertName), pemBytes)
}

// RootPEM returns cached or on-disk root.
func (h *AgentHelper) RootPEM() []byte {
	h.mu.RLock()
	if len(h.root) > 0 {
		out := append([]byte{}, h.root...)
		h.mu.RUnlock()
		return out
	}
	h.mu.RUnlock()
	dir, err := paths.Certs()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, RootCertName))
	if err != nil {
		return nil
	}
	h.mu.Lock()
	h.root = append([]byte{}, b...)
	h.mu.Unlock()
	return b
}

// EnsureRoot fetches rootCA from a registry if local cache is empty.
func (h *AgentHelper) EnsureRoot() ([]byte, error) {
	if b := h.RootPEM(); len(b) > 0 {
		return b, nil
	}
	if emb, ok := EmbeddedRootPEM(); ok {
		if err := h.SetRootCache(emb); err == nil {
			return emb, nil
		}
	}
	for _, base := range h.issueBases() {
		cli := &Client{BaseURL: base}
		pemBytes, err := cli.FetchRoot()
		if err != nil {
			if h.Buf != nil {
				h.Buf.Warnf("ca", "fetch root %s: %v", base, err)
			}
			continue
		}
		if err := h.SetRootCache(pemBytes); err != nil {
			return nil, err
		}
		if h.Buf != nil {
			h.Buf.Infof("ca", "rootCA baixada de %s", base)
		}
		return pemBytes, nil
	}
	return nil, fmt.Errorf("não foi possível obter rootCA de nenhum registry")
}

// EnsureLeaf issues (or reuses) a leaf cert covering domains into certDir.
func (h *AgentHelper) EnsureLeaf(domains []string, certDir string) error {
	domains, err := NormalizeDomains(domains)
	if err != nil {
		return err
	}
	if h.FilterDomains != nil {
		domains = h.FilterDomains(domains)
		if len(domains) == 0 {
			return fmt.Errorf("nenhum domínio registrado no ledger para esta conta — registre o .bl/.lo antes de pedir certificado CA")
		}
		domains, err = NormalizeDomains(domains)
		if err != nil {
			return err
		}
	}
	renewBefore := 7 * 24 * time.Hour
	if h.Cfg != nil {
		if d, err := time.ParseDuration(strings.TrimSpace(h.Cfg.CA.RenewBefore)); err == nil && d > 0 {
			renewBefore = d
		}
	}
	certPath := filepath.Join(certDir, LeafCertName)
	keyPath := filepath.Join(certDir, LeafKeyName)
	if leafOK(certPath, keyPath, domains, renewBefore) {
		return nil
	}
	if h.SignKey == nil {
		return fmt.Errorf("sem chave de conta — faça login para emitir certificado CA")
	}
	priv, err := h.SignKey()
	if err != nil {
		return fmt.Errorf("chave de conta: %w", err)
	}
	keyPEM, csrPEM, err := GenerateLeafKeyAndCSR(domains)
	if err != nil {
		return err
	}
	var lastErr error
	for _, base := range h.issueBases() {
		cli := &Client{BaseURL: base}
		resp, err := cli.Issue(priv, domains, csrPEM)
		if err != nil {
			lastErr = err
			if h.Buf != nil {
				h.Buf.Warnf("ca", "issue %s: %v", base, err)
			}
			continue
		}
		chain := []byte(resp.Chain)
		if len(chain) == 0 {
			chain = []byte(resp.Cert)
		}
		if err := WriteLeafMaterial(certDir, keyPEM, []byte(resp.Cert), chain); err != nil {
			return err
		}
		// cache root from chain if present
		if roots := extractRootFromChain(chain); len(roots) > 0 {
			_ = h.SetRootCache(roots)
		}
		if h.Buf != nil {
			h.Buf.Infof("ca", "leaf emitido por %s para %v", base, domains)
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("nenhum registry CA alcançável")
	}
	return lastErr
}

func (h *AgentHelper) issueBases() []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	if h.Cfg != nil {
		add(h.Cfg.CA.IssueURL)
	}
	if h.Resolve != nil {
		for _, u := range h.Resolve() {
			add(u)
		}
	}
	if h.Cfg != nil {
		for _, ip := range h.Cfg.Registry.StaticPeers {
			add(BaseURLFromYgg(ip, 9970))
		}
	}
	if ip := strings.TrimSpace(config.DefaultRegistryYggIP); ip != "" {
		add(BaseURLFromYgg(ip, 9970))
	}
	return out
}

func leafOK(certPath, keyPath string, wantDomains []string, renewBefore time.Duration) bool {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	if _, err := os.Stat(keyPath); err != nil {
		return false
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if time.Until(cert.NotAfter) < renewBefore {
		return false
	}
	have := map[string]bool{}
	for _, d := range cert.DNSNames {
		have[strings.ToLower(d)] = true
	}
	for _, d := range wantDomains {
		if !have[strings.ToLower(d)] {
			return false
		}
	}
	return true
}

func extractRootFromChain(chain []byte) []byte {
	rest := chain
	var last []byte
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			last = pem.EncodeToMemory(block)
			cert, err := x509.ParseCertificate(block.Bytes)
			if err == nil && cert.IsCA {
				last = pem.EncodeToMemory(block)
			}
		}
	}
	return last
}
