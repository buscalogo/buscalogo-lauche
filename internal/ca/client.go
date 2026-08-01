package ca

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client talks to a registry CA API over HTTP (typically via Ygg IPv6).
type Client struct {
	BaseURL    string // e.g. http://[200:...]:9970
	HTTPClient *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// FetchRoot downloads rootCA.pem from the registry.
func (c *Client) FetchRoot() ([]byte, error) {
	resp, err := c.http().Get(strings.TrimRight(c.BaseURL, "/") + "/api/ca/root")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/ca/root: %s: %s", resp.Status, bytes.TrimSpace(body))
	}
	if !bytes.Contains(body, []byte("BEGIN CERTIFICATE")) {
		return nil, fmt.Errorf("resposta não é um certificado PEM")
	}
	return body, nil
}

// Issue requests a leaf certificate signed by the registry CA.
func (c *Client) Issue(priv ed25519.PrivateKey, domains []string, csrPEM []byte) (*IssueResponse, error) {
	domains, err := NormalizeDomains(domains)
	if err != nil {
		return nil, err
	}
	pub := priv.Public().(ed25519.PublicKey)
	ts := time.Now().UnixMilli()
	sig, err := SignIssueWithKey(priv, pub, domains, csrPEM, ts)
	if err != nil {
		return nil, err
	}
	reqBody := IssueRequest{
		Domains:     domains,
		CSR:         string(csrPEM),
		OwnerPubKey: hex.EncodeToString(pub),
		Timestamp:   ts,
		Signature:   hex.EncodeToString(sig),
	}
	raw, _ := json.Marshal(reqBody)
	resp, err := c.http().Post(strings.TrimRight(c.BaseURL, "/")+"/api/ca/issue", "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST /api/ca/issue: %s: %s", resp.Status, bytes.TrimSpace(body))
	}
	var out IssueResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Cert == "" {
		return nil, fmt.Errorf("resposta sem cert")
	}
	return &out, nil
}

// BaseURLFromYgg builds http://[ip]:9970 for an IPv6 Ygg address.
func BaseURLFromYgg(ip string, port int) string {
	ip = strings.TrimSpace(ip)
	ip = strings.Trim(ip, "[]")
	if port <= 0 {
		port = 9970
	}
	if net.ParseIP(ip) == nil {
		return fmt.Sprintf("http://%s:%d", ip, port)
	}
	if strings.Contains(ip, ":") {
		return fmt.Sprintf("http://[%s]:%d", ip, port)
	}
	return fmt.Sprintf("http://%s:%d", ip, port)
}
