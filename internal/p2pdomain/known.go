package p2pdomain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"buscalogo-agent/internal/paths"
)

type knownAgentsFile struct {
	Agents []knownAgent `json:"agents"`
}

type knownAgent struct {
	YggIP     string    `json:"ygg_ip"`
	PeerID    string    `json:"peer_id,omitempty"`
	LastSeen  time.Time `json:"last_seen"`
	LastOK    time.Time `json:"last_ok,omitempty"`
}

func knownAgentsPath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "registry")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "known-agents.json"), nil
}

func loadKnownAgents() []knownAgent {
	path, err := knownAgentsPath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f knownAgentsFile
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	return f.Agents
}

func rememberAgent(yggIP, peerID string, ok bool) {
	yggIP = normalizeYggIP(yggIP)
	if yggIP == "" {
		return
	}
	path, err := knownAgentsPath()
	if err != nil {
		return
	}
	agents := loadKnownAgents()
	now := time.Now().UTC()
	found := false
	for i := range agents {
		if normalizeYggIP(agents[i].YggIP) != yggIP {
			continue
		}
		agents[i].LastSeen = now
		if peerID != "" {
			agents[i].PeerID = peerID
		}
		if ok {
			agents[i].LastOK = now
		}
		found = true
		break
	}
	if !found {
		a := knownAgent{YggIP: yggIP, PeerID: peerID, LastSeen: now}
		if ok {
			a.LastOK = now
		}
		agents = append(agents, a)
	}
	// Mantém no máximo 64 entradas, priorizando LastOK / LastSeen recentes.
	if len(agents) > 64 {
		agents = agents[len(agents)-64:]
	}
	raw, err := json.MarshalIndent(knownAgentsFile{Agents: agents}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o600)
}

func knownAgentIPs(self string) []string {
	self = normalizeYggIP(self)
	out := make([]string, 0)
	seen := map[string]bool{}
	for _, a := range loadKnownAgents() {
		ip := normalizeYggIP(a.YggIP)
		if ip == "" || ip == self || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip)
	}
	return out
}

func mergeUniqueIPs(self string, lists ...[]string) []string {
	self = normalizeYggIP(self)
	seen := map[string]bool{}
	if self != "" {
		seen[self] = true
	}
	out := make([]string, 0)
	for _, list := range lists {
		for _, raw := range list {
			ip := normalizeYggIP(raw)
			if ip == "" || seen[ip] {
				continue
			}
			seen[ip] = true
			out = append(out, ip)
		}
	}
	return out
}

func isBenignDialErr(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "network is unreachable") ||
		strings.Contains(msg, "no route to host")
}
