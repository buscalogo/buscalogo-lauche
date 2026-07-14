package api

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"buscalogo-agent/internal/ledger"
)

func (s *Server) handleRegistryLookup(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil {
		writeErr(w, http.StatusServiceUnavailable, "registry desabilitado")
		return
	}
	domain := ledger.NormalizeDomain(r.PathValue("domain"))
	rec, err := s.ledger.Lookup(domain)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if rec == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "não encontrado"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "record": rec})
}

func (s *Server) handleRegistryList(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil {
		writeErr(w, http.StatusServiceUnavailable, "registry desabilitado")
		return
	}
	recs, err := s.ledger.Store().ListDNS()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "domains": recs})
}

func (s *Server) handleRegistryStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"ok":      true,
		"enabled": s.ledger != nil,
		"engine":  s.cfg.Registry.Engine,
		"topic":   s.cfg.Registry.GossipTopic,
	}
	if s.p2pdomain != nil {
		out["gossip"] = s.p2pdomain.Status()
	}
	if s.ygg != nil {
		out["ygg_ip"] = s.ygg.SelfAddress()
		out["ygg_peers"] = s.ygg.PeerAddresses()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRegistryBootstrap(w http.ResponseWriter, r *http.Request) {
	yggIP := ""
	if s.ygg != nil {
		yggIP = s.ygg.SelfAddress()
	}
	out := map[string]any{
		"ok":            true,
		"ygg_ip":        yggIP,
		"discover_port": s.cfg.Registry.ListenPort + 1,
		"libp2p_port":   s.cfg.Registry.ListenPort,
		"topic":         s.cfg.Registry.GossipTopic,
		"hint":          "Nos Agents: registry.static_peers=[ygg_ip] ou compile com -X ...DefaultRegistryYggIP=<ygg_ip>",
	}
	if s.p2pdomain != nil {
		st := s.p2pdomain.Status()
		out["gossip"] = st
		if yggIP == "" {
			if id, ok := st["peer_id"].(string); ok {
				out["peer_id"] = id
			}
		}
	}
	if yggIP == "" {
		out["ok"] = false
		out["error"] = "Ygg IPv6 ainda indisponível"
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRegistrySync(w http.ResponseWriter, r *http.Request) {
	if s.p2pdomain == nil {
		writeErr(w, http.StatusServiceUnavailable, "gossip desabilitado")
		return
	}
	res := s.p2pdomain.SyncNow(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"message":   "sync concluído",
		"tried":     res.Tried,
		"connected": res.Connected,
		"applied":   res.Applied,
		"peers":     res.Peers,
		"errors":    res.Errors,
		"gossip":    s.p2pdomain.Status(),
	})
}

func (s *Server) handleRegistryAddPeer(w http.ResponseWriter, r *http.Request) {
	if s.p2pdomain == nil {
		writeErr(w, http.StatusServiceUnavailable, "gossip desabilitado")
		return
	}
	var body struct {
		YggIP string `json:"ygg_ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	ip, err := s.p2pdomain.AddStaticPeer(body.YggIP)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "%v", err)
		return
	}
	peers := append([]string(nil), s.cfg.Snapshot().Registry.StaticPeers...)
	found := false
	for _, p := range peers {
		if p == ip {
			found = true
			break
		}
	}
	if !found {
		peers = append(peers, ip)
		raw, _ := json.Marshal(map[string]any{"registry": map[string]any{"static_peers": peers}})
		_ = s.cfg.MergeJSON(raw)
		_ = s.cfg.Save()
	}

	res := s.p2pdomain.SyncNow(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"message":   "peer remoto adicionado",
		"ygg_ip":    ip,
		"tried":     res.Tried,
		"connected": res.Connected,
		"applied":   res.Applied,
		"peers":     res.Peers,
		"errors":    res.Errors,
		"gossip":    s.p2pdomain.Status(),
	})
}

type registryRegisterBody struct {
	Domain string   `json:"domain"`
	AAAA   []string `json:"aaaa"`
	A      []string `json:"a"`
	TXT    []string `json:"txt"`
	TTL    int      `json:"ttl"`
}

func (s *Server) handleRegistryRegister(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil || s.account == nil {
		writeErr(w, http.StatusServiceUnavailable, "registry ou conta indisponível")
		return
	}
	if s.accountFromRequest(r) == nil {
		writeErr(w, http.StatusUnauthorized, "faça login")
		return
	}
	priv, err := s.account.SigningKey()
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "%v", err)
		return
	}
	var body registryRegisterBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	domain := ledger.NormalizeDomain(body.Domain)
	if !ledger.ValidDomain(domain) {
		writeErr(w, http.StatusBadRequest, "domínio inválido (use nome.bl)")
		return
	}
	aaaa := body.AAAA
	if len(aaaa) == 0 {
		if s.ygg != nil {
			if ip := strings.TrimSpace(s.ygg.SelfAddress()); ip != "" {
				aaaa = []string{ip}
			}
		}
	}
	if len(aaaa) == 0 && len(body.A) == 0 {
		writeErr(w, http.StatusBadRequest, "sem AAAA Ygg — aguarde Yggdrasil ou informe aaaa")
		return
	}
	// Fluxo público: site deve ser alcançável na mesh.
	_ = s.cfg.EnsureWebExternalListen()

	recs := ledger.Records{AAAA: aaaa, A: body.A, TXT: body.TXT, TTL: body.TTL}
	ev, err := s.ledger.SignAndApply(priv, ledger.TypeRegister, domain, recs, nil)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "%v", err)
		return
	}
	// Empurra o evento ao registry/peers (pull sozinho não publica registros locais).
	if s.p2pdomain != nil {
		s.p2pdomain.ForceSync()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"event":  eventView(ev),
		"record": mustLookup(s, domain),
		"hint":   "Sync enviado aos peers/registry; sirva o site com portas Ygg abertas.",
	})
}

func (s *Server) handleRegistryUpdate(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil || s.account == nil {
		writeErr(w, http.StatusServiceUnavailable, "registry ou conta indisponível")
		return
	}
	if s.accountFromRequest(r) == nil {
		writeErr(w, http.StatusUnauthorized, "faça login")
		return
	}
	priv, err := s.account.SigningKey()
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "%v", err)
		return
	}
	var body registryRegisterBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	domain := ledger.NormalizeDomain(r.PathValue("domain"))
	if body.Domain != "" {
		domain = ledger.NormalizeDomain(body.Domain)
	}
	aaaa := body.AAAA
	if len(aaaa) == 0 && s.ygg != nil {
		if ip := strings.TrimSpace(s.ygg.SelfAddress()); ip != "" {
			aaaa = []string{ip}
		}
	}
	s.cfg.EnsureWebExternalListen()
	ev, err := s.ledger.SignAndApply(priv, ledger.TypeUpdate, domain, ledger.Records{
		AAAA: aaaa, A: body.A, TXT: body.TXT, TTL: body.TTL,
	}, nil)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "%v", err)
		return
	}
	if s.p2pdomain != nil {
		s.p2pdomain.ForceSync()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": eventView(ev), "record": mustLookup(s, domain)})
}

type transferBody struct {
	TargetPubkeyHex string `json:"target_pubkey"`
}

func (s *Server) handleRegistryTransfer(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil || s.account == nil {
		writeErr(w, http.StatusServiceUnavailable, "registry ou conta indisponível")
		return
	}
	if s.accountFromRequest(r) == nil {
		writeErr(w, http.StatusUnauthorized, "faça login")
		return
	}
	priv, err := s.account.SigningKey()
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "%v", err)
		return
	}
	var body transferBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	raw, err := hex.DecodeString(strings.TrimSpace(body.TargetPubkeyHex))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		writeErr(w, http.StatusBadRequest, "target_pubkey hex inválida (64 bytes ed25519)")
		return
	}
	domain := ledger.NormalizeDomain(r.PathValue("domain"))
	ev, err := s.ledger.SignAndApply(priv, ledger.TypeTransfer, domain, ledger.Records{}, raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": eventView(ev), "record": mustLookup(s, domain)})
}

func eventView(ev *ledger.DomainEvent) map[string]any {
	if ev == nil {
		return nil
	}
	return map[string]any{
		"type":         ledger.TypeName(ev.Type),
		"domain":       ev.Domain,
		"nonce":        ev.Nonce,
		"timestamp":    ev.Timestamp,
		"owner":        hex.EncodeToString(ev.OwnerPubkey),
		"target":       hex.EncodeToString(ev.TargetPubkey),
		"aaaa":         ev.Records.AAAA,
		"a":            ev.Records.A,
		"hash":         hex.EncodeToString(ev.Hash()),
	}
}

func mustLookup(s *Server, domain string) any {
	rec, _ := s.ledger.Lookup(domain)
	return rec
}

// handleDNSJSON implementa DoH JSON (aplication/dns-json) para .bl via ledger local.
func (s *Server) handleDNSJSON(w http.ResponseWriter, r *http.Request) {
	name := ledger.NormalizeDomain(r.URL.Query().Get("name"))
	name = strings.TrimSuffix(name, ".")
	qtype := strings.ToUpper(r.URL.Query().Get("type"))
	if qtype == "" {
		qtype = "AAAA"
	}
	if s.ledger == nil || !strings.HasSuffix(name, ".bl") {
		writeJSON(w, http.StatusOK, map[string]any{
			"Status": 3, "Question": []map[string]any{{"name": name, "type": qtype}}, "Answer": []any{},
		})
		return
	}
	dnsRec, err := s.ledger.Store().GetDNS(name)
	if err != nil || dnsRec == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"Status": 3, "Question": []map[string]any{{"name": name, "type": qtype}}, "Answer": []any{},
		})
		return
	}
	ttl := dnsRec.TTL
	if ttl == 0 {
		ttl = 300
	}
	var answers []map[string]any
	switch qtype {
	case "AAAA", "28":
		for _, a := range dnsRec.AAAA {
			if net.ParseIP(a) == nil {
				continue
			}
			answers = append(answers, map[string]any{"name": name, "type": 28, "TTL": ttl, "data": a})
		}
	case "A", "1":
		for _, a := range dnsRec.A {
			if net.ParseIP(a) == nil {
				continue
			}
			answers = append(answers, map[string]any{"name": name, "type": 1, "TTL": ttl, "data": a})
		}
	case "TXT", "16":
		for _, t := range dnsRec.TXT {
			answers = append(answers, map[string]any{"name": name, "type": 16, "TTL": ttl, "data": t})
		}
	}
	status := 0
	if len(answers) == 0 {
		status = 3
	}
	w.Header().Set("Content-Type", "application/dns-json")
	writeJSON(w, http.StatusOK, map[string]any{
		"Status":   status,
		"Question": []map[string]any{{"name": name, "type": qtype}},
		"Answer":   answers,
	})
}
