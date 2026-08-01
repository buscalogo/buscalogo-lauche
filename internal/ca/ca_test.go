package ca_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"path/filepath"
	"testing"
	"time"

	"buscalogo-agent/internal/ca"
)

func TestEnsureRootSignCSR(t *testing.T) {
	dir := t.TempDir()
	auth, err := ca.EnsureRootAllowGenerate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Cert.IsCA {
		t.Fatal("expected CA")
	}
	// reload without generating
	auth2, err := ca.EnsureRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if auth2.Cert.SerialNumber.Cmp(auth.Cert.SerialNumber) != 0 {
		t.Fatal("should reuse existing CA")
	}
	if auth.FingerprintSHA256() == "" || auth.FingerprintSHA256() != auth2.FingerprintSHA256() {
		t.Fatal("fingerprint mismatch")
	}

	empty := t.TempDir()
	if _, err := ca.EnsureRoot(empty); err == nil {
		t.Fatal("EnsureRoot should fail without CA files")
	}

	keyPEM, csrPEM, err := ca.GenerateLeafKeyAndCSR([]string{"loja.bl", "loja.lo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(keyPEM) == 0 {
		t.Fatal("empty key")
	}
	certPEM, chainPEM, err := auth.SignCSR(csrPEM, []string{"loja.bl", "loja.lo"}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(auth.Cert)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "loja.bl", Roots: roots}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !bytesContains(chainPEM, auth.PEM) {
		t.Fatal("chain should include root")
	}
	out := filepath.Join(dir, "leaf")
	if err := ca.WriteLeafMaterial(out, keyPEM, certPEM, chainPEM); err != nil {
		t.Fatal(err)
	}
}

func TestIssueRequestSignVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, csrPEM, err := ca.GenerateLeafKeyAndCSR([]string{"a.bl"})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UnixMilli()
	sig, err := ca.SignIssueWithKey(priv, pub, []string{"a.bl"}, csrPEM, ts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.VerifyIssueRequest(pub, []string{"a.bl"}, csrPEM, ts, sig); err != nil {
		t.Fatal(err)
	}
	if err := ca.VerifyIssueRequest(pub, []string{"a.bl"}, csrPEM, ts-ca.IssueSkew.Milliseconds()-1000, sig); err == nil {
		t.Fatal("expected skew error")
	}
}

func bytesContains(hay, needle []byte) bool {
	return len(needle) > 0 && len(hay) >= len(needle) && (string(hay) == string(needle) || len(hay) > 0 && contains(hay, needle))
}

func contains(hay, needle []byte) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		ok := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
