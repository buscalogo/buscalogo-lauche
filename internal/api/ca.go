package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"buscalogo-agent/internal/ca"
	"buscalogo-agent/internal/ledger"
)

func (s *Server) SetCA(auth *ca.Authority) {
	s.ca = auth
}

func (s *Server) handleCARoot(w http.ResponseWriter, r *http.Request) {
	pemBytes, err := s.rootCAPEM()
	if err != nil || len(pemBytes) == 0 {
		writeErr(w, http.StatusServiceUnavailable, "CA raiz indisponível")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="buscalogo-rootCA.pem"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pemBytes)
}

func (s *Server) handleCAStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"system_trust_installed": ca.SystemTrustInstalled(),
		"leaf_mode":              "",
		"tls_running":            false,
	}
	if s.ca != nil {
		for k, v := range s.ca.Status() {
			out[k] = v
		}
		if s.ca.CanIssue() {
			out["role"] = "issuer"
		} else {
			out["role"] = "registry"
		}
	} else {
		out["ready"] = false
		out["can_issue"] = false
		out["role"] = "client"
		if pem, err := s.rootCAPEM(); err == nil && len(pem) > 0 {
			out["root_available"] = true
		}
	}
	if s.sites != nil {
		running, port, errMsg, mode := s.sites.TLSStatus()
		out["tls_running"] = running
		out["tls_port"] = port
		out["tls_error"] = errMsg
		out["leaf_mode"] = mode
		if info := s.sites.LeafCertInfo(); info != nil {
			for k, v := range info {
				out[k] = v
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCAIssue(w http.ResponseWriter, r *http.Request) {
	if s.ca == nil || !s.ca.CanIssue() {
		writeErr(w, http.StatusServiceUnavailable, "este registry não assina certificados (sem rootCA-key) — tente outro emissor na mesh")
		return
	}
	if s.ledger == nil {
		writeErr(w, http.StatusServiceUnavailable, "ledger indisponível")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ler body: %v", err)
		return
	}
	var req ca.IssueRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	domains, err := ca.NormalizeDomains(req.Domains)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "%v", err)
		return
	}
	ownerPub, err := ca.DecodeHex(req.OwnerPubKey)
	if err != nil || len(ownerPub) != 32 {
		writeErr(w, http.StatusBadRequest, "owner_pubkey inválida")
		return
	}
	sig, err := ca.DecodeHex(req.Signature)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "signature inválida")
		return
	}
	csrPEM := []byte(req.CSR)
	if err := ca.VerifyIssueRequest(ownerPub, domains, csrPEM, req.Timestamp, sig); err != nil {
		writeErr(w, http.StatusUnauthorized, "%v", err)
		return
	}
	for _, d := range domains {
		dns, err := s.ledger.Store().GetDNS(ledger.NormalizeDomain(d))
		if err != nil || dns == nil {
			writeErr(w, http.StatusForbidden, "domínio não registrado: %s", d)
			return
		}
		if !bytes.Equal(dns.Owner, ownerPub) {
			writeErr(w, http.StatusForbidden, "não é owner de %s", d)
			return
		}
	}
	certPEM, chainPEM, err := s.ca.SignCSR(csrPEM, domains, 90*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "assinar: %v", err)
		return
	}
	s.buf.Infof("ca", "emitido leaf para %v", domains)
	writeJSON(w, http.StatusOK, ca.IssueResponse{
		Cert:  string(certPEM),
		Chain: string(chainPEM),
	})
}

func (s *Server) handleCAInstallTrust(w http.ResponseWriter, r *http.Request) {
	// Garante root em cache (embutida ou baixada do registry).
	if s.sites != nil {
		_ = s.sites.CachedRootPEM()
	}
	pemBytes, err := s.rootCAPEM()
	if err != nil || len(pemBytes) == 0 {
		if emb, ok := ca.EmbeddedRootPEM(); ok {
			pemBytes = emb
		}
	}
	if len(pemBytes) == 0 {
		writeErr(w, http.StatusServiceUnavailable, "rootCA.pem indisponível — sincronize com um registry ou reinstale o Agent")
		return
	}
	if err := ca.InstallSystemTrust(s.buf, pemBytes); err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	s.buf.Infof("ca", "rootCA instalada no trust store do sistema")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                     true,
		"system_trust_installed": ca.SystemTrustInstalled(),
	})
}

func (s *Server) handleCARenew(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeErr(w, http.StatusServiceUnavailable, "sites indisponível")
		return
	}
	if err := s.sites.RenewTLSCert(); err != nil {
		writeErr(w, http.StatusBadGateway, "%v", err)
		return
	}
	running, port, errMsg, mode := s.sites.TLSStatus()
	out := map[string]any{
		"ok":          true,
		"tls_running": running,
		"tls_port":    port,
		"tls_error":   errMsg,
		"tls_mode":    mode,
	}
	if info := s.sites.LeafCertInfo(); info != nil {
		for k, v := range info {
			out[k] = v
		}
		if caSigned, _ := info["leaf_ca_signed"].(bool); !caSigned {
			writeErr(w, http.StatusBadGateway, "leaf ainda não assinado pela rootCA — registry emissor inacessível?")
			return
		}
		out["tls_mode"] = "ca"
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) rootCAPEM() ([]byte, error) {
	if s.ca != nil && len(s.ca.PEM) > 0 {
		return s.ca.PEM, nil
	}
	if s.sites != nil {
		if b := s.sites.CachedRootPEM(); len(b) > 0 {
			return b, nil
		}
	}
	return nil, errNoRoot
}

type noRootError struct{}

func (noRootError) Error() string { return "sem root CA" }

var errNoRoot = noRootError{}
