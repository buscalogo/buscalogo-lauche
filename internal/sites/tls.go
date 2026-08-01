package sites

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"buscalogo-agent/internal/ca"
	"buscalogo-agent/internal/paths"
)

const (
	defaultCertName = "site.crt"
	defaultKeyName  = "site.key"
)

// TLSStatus resume o HTTPS dos sites.
func (m *Manager) TLSStatus() (running bool, port int, errMsg, mode string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tlsRunning, m.tlsPort, m.tlsError, m.tlsMode
}

func (m *Manager) certDir() (string, error) {
	dir := m.cfg.Web.TLS.CertDir
	if dir != "" {
		if filepath.IsAbs(dir) {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
			return dir, nil
		}
		data, err := paths.Data()
		if err != nil {
			return "", err
		}
		full := filepath.Join(data, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			return "", err
		}
		return full, nil
	}
	return paths.Certs()
}

func (m *Manager) certPaths() (certFile, keyFile string, err error) {
	if m.cfg.Web.TLS.CertFile != "" && m.cfg.Web.TLS.KeyFile != "" {
		return m.cfg.Web.TLS.CertFile, m.cfg.Web.TLS.KeyFile, nil
	}
	dir, err := m.certDir()
	if err != nil {
		return "", "", err
	}
	// Prefer chain for serving when present (leaf+root).
	chain := filepath.Join(dir, ca.ChainName)
	if _, err := os.Stat(chain); err == nil {
		return chain, filepath.Join(dir, ca.LeafKeyName), nil
	}
	return filepath.Join(dir, defaultCertName), filepath.Join(dir, defaultKeyName), nil
}

func (m *Manager) tlsDomains() []string {
	hosts := []string{}
	m.mu.RLock()
	for _, s := range m.sites {
		if s.Enabled && s.Host != "" {
			hosts = append(hosts, s.Host)
		}
	}
	m.mu.RUnlock()
	if len(hosts) == 0 {
		hosts = []string{"buscalogo.bl"}
	}
	return hosts
}

func (m *Manager) ensureTLSMaterial() (certFile, keyFile string, err error) {
	certFile, keyFile, err = m.certPaths()
	if err != nil {
		return "", "", err
	}
	mode := m.cfg.Web.TLS.Mode
	if mode == "" {
		mode = "ca"
	}
	m.mu.Lock()
	issuer := m.issuer
	m.mu.Unlock()

	switch mode {
	case "off":
		return "", "", fmt.Errorf("HTTPS desabilitado")
	case "files":
		if _, errC := os.Stat(certFile); errC != nil {
			return "", "", fmt.Errorf("cert/key ausentes (mode=files)")
		}
		if _, errK := os.Stat(keyFile); errK != nil {
			return "", "", fmt.Errorf("cert/key ausentes (mode=files)")
		}
		m.mu.Lock()
		m.tlsMode = "files"
		m.mu.Unlock()
		return certFile, keyFile, nil
	case "ca":
		if issuer != nil {
			dir, err := m.certDir()
			if err != nil {
				return "", "", err
			}
			domains := m.tlsDomains()
			if err := issuer.EnsureLeaf(domains, dir); err != nil {
				m.buf.Warnf("sites", "CA issue falhou (%v) — fallback self_signed", err)
				if err := m.writeSelfSigned(filepath.Join(dir, defaultCertName), filepath.Join(dir, defaultKeyName)); err != nil {
					return "", "", err
				}
				m.mu.Lock()
				m.tlsMode = "self_signed"
				m.mu.Unlock()
				return filepath.Join(dir, defaultCertName), filepath.Join(dir, defaultKeyName), nil
			}
			m.mu.Lock()
			m.tlsMode = "ca"
			m.mu.Unlock()
			// refresh paths (chain may now exist)
			return m.certPaths()
		}
		m.buf.Warnf("sites", "mode=ca sem issuer — self_signed")
		fallthrough
	default: // self_signed
		dir := filepath.Dir(certFile)
		leafCert := filepath.Join(dir, defaultCertName)
		leafKey := filepath.Join(dir, defaultKeyName)
		_, errC := os.Stat(leafCert)
		_, errK := os.Stat(leafKey)
		if errC != nil || errK != nil {
			if err := m.writeSelfSigned(leafCert, leafKey); err != nil {
				return "", "", err
			}
			m.buf.Infof("sites", "TLS self-signed criado em %s", dir)
		}
		m.mu.Lock()
		m.tlsMode = "self_signed"
		m.mu.Unlock()
		return leafCert, leafKey, nil
	}
}

func (m *Manager) writeSelfSigned(certFile, keyFile string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return err
	}
	hosts := append([]string{"localhost"}, m.tlsDomains()...)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"BuscaLogo"},
			CommonName:   "BuscaLogo .bl/.lo (self-signed)",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour * 3),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              uniqueStrings(hosts),
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o755); err != nil {
		return err
	}
	certOut, err := os.OpenFile(certFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		_ = certOut.Close()
		return err
	}
	_ = certOut.Close()

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyOut, err := os.OpenFile(keyFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		_ = keyOut.Close()
		return err
	}
	return keyOut.Close()
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func (m *Manager) startTLS() {
	if !m.cfg.Web.TLS.Enabled || m.cfg.Web.TLS.Mode == "off" {
		m.buf.Infof("sites", "HTTPS desabilitado (web.tls.enabled=false)")
		return
	}
	certFile, keyFile, err := m.ensureTLSMaterial()
	if err != nil {
		m.mu.Lock()
		m.tlsRunning = false
		m.tlsError = err.Error()
		m.mu.Unlock()
		m.buf.Warnf("sites", "HTTPS: %v", err)
		return
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		m.mu.Lock()
		m.tlsRunning = false
		m.tlsError = err.Error()
		m.mu.Unlock()
		m.buf.Errorf("sites", "carregar cert TLS: %v", err)
		return
	}

	port := m.cfg.Web.TLS.Port
	if port == 0 {
		port = 443
	}
	if m.tryListenTLS(port, cert, false) {
		return
	}
	if port == 443 {
		m.tryListenTLS(8443, cert, true)
	}
}

func (m *Manager) tryListenTLS(port int, cert tls.Certificate, fallback bool) bool {
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}
	handler := m.Handler()
	for _, addr := range m.listenAddrs(port) {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			m.buf.Warnf("sites", "bind TLS %s: %v", addr, err)
			continue
		}
		ln = tls.NewListener(ln, tlsCfg)
		srv := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			TLSConfig:         tlsCfg,
		}
		m.mu.Lock()
		m.srvTLS = srv
		m.tlsPort = port
		m.tlsRunning = true
		m.tlsError = ""
		mode := m.tlsMode
		m.mu.Unlock()

		if fallback {
			m.buf.Warnf("sites", "HTTPS em %s (fallback — :443 indisponível; mode=%s)", addr, mode)
		} else {
			m.buf.Infof("sites", "HTTPS em %s (mode=%s)", addr, mode)
		}

		err = srv.Serve(ln)
		m.mu.Lock()
		m.tlsRunning = false
		if err != nil && err != http.ErrServerClosed {
			m.tlsError = err.Error()
			m.buf.Errorf("sites", "HTTPS em %s: %v", addr, err)
		}
		m.mu.Unlock()
		return true
	}
	msg := fmt.Sprintf("não foi possível abrir HTTPS :%d (ocupada ou sem permissão)", port)
	m.mu.Lock()
	m.tlsRunning = false
	m.tlsError = msg
	m.mu.Unlock()
	m.buf.Errorf("sites", "%s", msg)
	return false
}
