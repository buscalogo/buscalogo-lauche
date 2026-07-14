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
	return filepath.Join(dir, defaultCertName), filepath.Join(dir, defaultKeyName), nil
}

func (m *Manager) ensureTLSMaterial() (certFile, keyFile string, err error) {
	certFile, keyFile, err = m.certPaths()
	if err != nil {
		return "", "", err
	}
	mode := m.cfg.Web.TLS.Mode
	if mode == "" {
		mode = "self_signed"
	}
	m.mu.Lock()
	m.tlsMode = mode
	m.mu.Unlock()

	_, errC := os.Stat(certFile)
	_, errK := os.Stat(keyFile)
	if errC == nil && errK == nil {
		return certFile, keyFile, nil
	}
	if mode == "files" || mode == "ca" {
		return "", "", fmt.Errorf("cert/key ausentes em %s (mode=%s) — aguarde CA ou coloque site.crt/site.key", filepath.Dir(certFile), mode)
	}
	// self_signed: gera placeholder até existir CA BuscaLogo.
	if err := m.writeSelfSigned(certFile, keyFile); err != nil {
		return "", "", err
	}
	m.buf.Infof("sites", "TLS self-signed criado em %s (substituível por CA depois)", filepath.Dir(certFile))
	_ = os.WriteFile(filepath.Join(filepath.Dir(certFile), "README.md"), []byte(`# Certificados HTTPS .bl

site.crt / site.key — self-signed gerado pelo Agent.
Próximo passo: CA BuscaLogo (web.tls.mode=ca) e root nos clientes.
`), 0o644)
	return certFile, keyFile, nil
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
	hosts := []string{"localhost", "*.bl"}
	m.mu.RLock()
	for _, s := range m.sites {
		if s.Enabled && s.Host != "" {
			hosts = append(hosts, s.Host)
		}
	}
	m.mu.RUnlock()

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"BuscaLogo"},
			CommonName:   "BuscaLogo .bl (self-signed — pré-CA)",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour * 3), // 3 anos
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
		m.mu.Unlock()

		if fallback {
			m.buf.Warnf("sites", "HTTPS em %s (fallback — :443 indisponível; self-signed até CA)", addr)
		} else {
			m.buf.Infof("sites", "HTTPS em %s (mode=%s — CA BuscaLogo depois)", addr, m.cfg.Web.TLS.Mode)
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
