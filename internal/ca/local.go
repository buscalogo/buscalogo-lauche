package ca

import (
	"os"
	"path/filepath"
	"time"
)

// LocalIssuer signs leaves with an in-process Authority (registry node).
type LocalIssuer struct {
	Auth *Authority
	Buf  interface {
		Infof(component, format string, args ...any)
		Warnf(component, format string, args ...any)
	}
}

func (l *LocalIssuer) EnsureRoot() ([]byte, error) {
	if l.Auth == nil {
		return nil, errNoAuth
	}
	return l.Auth.PEM, nil
}

func (l *LocalIssuer) RootPEM() []byte {
	if l.Auth == nil {
		return nil
	}
	return l.Auth.PEM
}

func (l *LocalIssuer) EnsureLeaf(domains []string, certDir string) error {
	if l.Auth == nil {
		return errNoAuth
	}
	domains, err := NormalizeDomains(domains)
	if err != nil {
		return err
	}
	rootPEM := l.RootPEM()
	if leafOK(filepath.Join(certDir, LeafCertName), filepath.Join(certDir, LeafKeyName), domains, 7*24*time.Hour, rootPEM) {
		return nil
	}
	return l.issueLeaf(domains, certDir)
}

// ForceEnsureLeaf always re-signs a leaf with the local CA.
func (l *LocalIssuer) ForceEnsureLeaf(domains []string, certDir string) error {
	_ = os.Remove(filepath.Join(certDir, LeafCertName))
	_ = os.Remove(filepath.Join(certDir, LeafKeyName))
	_ = os.Remove(filepath.Join(certDir, ChainName))
	domains, err := NormalizeDomains(domains)
	if err != nil {
		return err
	}
	return l.issueLeaf(domains, certDir)
}

func (l *LocalIssuer) issueLeaf(domains []string, certDir string) error {
	if l.Auth == nil {
		return errNoAuth
	}
	keyPEM, csrPEM, err := GenerateLeafKeyAndCSR(domains)
	if err != nil {
		return err
	}
	certPEM, chainPEM, err := l.Auth.SignCSR(csrPEM, domains, DefaultLeafTTL)
	if err != nil {
		return err
	}
	if err := WriteLeafMaterial(certDir, keyPEM, certPEM, chainPEM); err != nil {
		return err
	}
	if l.Buf != nil {
		l.Buf.Infof("ca", "leaf local para %v", domains)
	}
	return nil
}

type noAuthError struct{}

func (noAuthError) Error() string { return "CA local ausente" }

var errNoAuth = noAuthError{}
