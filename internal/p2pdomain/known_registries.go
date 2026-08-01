package p2pdomain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"buscalogo-agent/internal/paths"
)

const (
	StatusOnline  = "online"
	StatusStale   = "stale"
	StatusOffline = "offline"

	SourceBootstrapHTTP = "bootstrap_http"
	SourceStatic        = "static"
	SourceExchange      = "exchange"
	SourceDiscover      = "discover"

	maxKnownRegistries = 128
	RoleRegistry       = "registry"
)

// PeerHealth holds TTL knobs for registry mesh status.
type PeerHealth struct {
	StaleAfter   time.Duration
	OfflineAfter time.Duration
	PruneAfter   time.Duration
}

func DefaultPeerHealth() PeerHealth {
	return PeerHealth{
		StaleAfter:   5 * time.Minute,
		OfflineAfter: time.Hour,
		PruneAfter:   24 * time.Hour,
	}
}

type knownRegistriesFile struct {
	Registries []knownRegistry `json:"registries"`
}

type knownRegistry struct {
	YggIP    string    `json:"ygg_ip"`
	PeerID   string    `json:"peer_id,omitempty"`
	Role     string    `json:"role"`
	Source   string    `json:"source,omitempty"`
	LastSeen time.Time `json:"last_seen"`
	LastOK   time.Time `json:"last_ok,omitempty"`
	Status   string    `json:"status"`
	Failures int       `json:"failures,omitempty"`
}

// RegistryPeerInfo is the public view for API / scripts.
type RegistryPeerInfo struct {
	YggIP    string `json:"ygg_ip"`
	PeerID   string `json:"peer_id,omitempty"`
	Role     string `json:"role"`
	Source   string `json:"source,omitempty"`
	LastSeen string `json:"last_seen,omitempty"`
	LastOK   string `json:"last_ok,omitempty"`
	Status   string `json:"status"`
	Failures int    `json:"failures,omitempty"`
}

func knownRegistriesPath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "registry")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "known-registries.json"), nil
}

func loadKnownRegistries() []knownRegistry {
	path, err := knownRegistriesPath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f knownRegistriesFile
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	return f.Registries
}

func saveKnownRegistries(list []knownRegistry) {
	path, err := knownRegistriesPath()
	if err != nil {
		return
	}
	if len(list) > maxKnownRegistries {
		sort.SliceStable(list, func(i, j int) bool {
			ti, tj := list[i].LastOK, list[j].LastOK
			if ti.IsZero() {
				ti = list[i].LastSeen
			}
			if tj.IsZero() {
				tj = list[j].LastSeen
			}
			return ti.After(tj)
		})
		list = list[:maxKnownRegistries]
	}
	raw, err := json.MarshalIndent(knownRegistriesFile{Registries: list}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o600)
}

func computeRegistryStatus(r knownRegistry, now time.Time, h PeerHealth) string {
	if h.StaleAfter <= 0 {
		h = DefaultPeerHealth()
	}
	ref := r.LastOK
	if ref.IsZero() {
		ref = r.LastSeen
	}
	if ref.IsZero() {
		return StatusOffline
	}
	age := now.Sub(ref)
	switch {
	case age <= h.StaleAfter:
		return StatusOnline
	case age <= h.OfflineAfter:
		return StatusStale
	default:
		return StatusOffline
	}
}

func refreshRegistryStatuses(list []knownRegistry, now time.Time, h PeerHealth) []knownRegistry {
	out := make([]knownRegistry, 0, len(list))
	for _, r := range list {
		st := computeRegistryStatus(r, now, h)
		if st == StatusOffline && h.PruneAfter > 0 {
			ref := r.LastOK
			if ref.IsZero() {
				ref = r.LastSeen
			}
			if !ref.IsZero() && now.Sub(ref) > h.PruneAfter {
				continue
			}
		}
		r.Status = st
		out = append(out, r)
	}
	return out
}

// rememberRegistry upserts a registry peer. ok=true means successful sync/dial.
func rememberRegistry(yggIP, peerID, source string, ok bool, h PeerHealth) {
	yggIP = normalizeYggIP(yggIP)
	if yggIP == "" {
		return
	}
	if h.StaleAfter <= 0 {
		h = DefaultPeerHealth()
	}
	list := loadKnownRegistries()
	now := time.Now().UTC()
	found := false
	for i := range list {
		if normalizeYggIP(list[i].YggIP) != yggIP {
			continue
		}
		list[i].LastSeen = now
		list[i].Role = RoleRegistry
		if peerID != "" {
			list[i].PeerID = peerID
		}
		if source != "" && list[i].Source == "" {
			list[i].Source = source
		}
		if ok {
			list[i].LastOK = now
			list[i].Failures = 0
		} else {
			list[i].Failures++
		}
		list[i].Status = computeRegistryStatus(list[i], now, h)
		found = true
		break
	}
	if !found {
		r := knownRegistry{
			YggIP:    yggIP,
			PeerID:   peerID,
			Role:     RoleRegistry,
			Source:   source,
			LastSeen: now,
			Status:   StatusStale,
		}
		if ok {
			r.LastOK = now
			r.Status = StatusOnline
		} else {
			r.Failures = 1
			r.Status = StatusOffline
		}
		list = append(list, r)
	}
	list = refreshRegistryStatuses(list, now, h)
	saveKnownRegistries(list)
}

func markRegistryDialFailure(yggIP string, h PeerHealth) {
	yggIP = normalizeYggIP(yggIP)
	if yggIP == "" {
		return
	}
	list := loadKnownRegistries()
	now := time.Now().UTC()
	changed := false
	for i := range list {
		if normalizeYggIP(list[i].YggIP) != yggIP {
			continue
		}
		list[i].Failures++
		list[i].LastSeen = now
		list[i].Status = computeRegistryStatus(list[i], now, h)
		changed = true
		break
	}
	if changed {
		list = refreshRegistryStatuses(list, now, h)
		saveKnownRegistries(list)
	}
}

func mergeRegistryPeers(remote []knownRegistry, source string, self string, h PeerHealth) int {
	self = normalizeYggIP(self)
	if h.StaleAfter <= 0 {
		h = DefaultPeerHealth()
	}
	list := loadKnownRegistries()
	now := time.Now().UTC()
	byIP := map[string]int{}
	for i, r := range list {
		byIP[normalizeYggIP(r.YggIP)] = i
	}
	added := 0
	for _, in := range remote {
		ip := normalizeYggIP(in.YggIP)
		if ip == "" || ip == self {
			continue
		}
		src := source
		if src == "" {
			src = SourceExchange
		}
		if idx, ok := byIP[ip]; ok {
			if in.PeerID != "" {
				list[idx].PeerID = in.PeerID
			}
			if in.LastOK.After(list[idx].LastOK) {
				list[idx].LastOK = in.LastOK
			}
			if in.LastSeen.After(list[idx].LastSeen) {
				list[idx].LastSeen = in.LastSeen
			}
			if list[idx].Source == "" {
				list[idx].Source = src
			}
			list[idx].Role = RoleRegistry
			list[idx].Status = computeRegistryStatus(list[idx], now, h)
			continue
		}
		r := knownRegistry{
			YggIP:    ip,
			PeerID:   in.PeerID,
			Role:     RoleRegistry,
			Source:   src,
			LastSeen: in.LastSeen,
			LastOK:   in.LastOK,
		}
		if r.LastSeen.IsZero() {
			r.LastSeen = now
		}
		r.Status = computeRegistryStatus(r, now, h)
		list = append(list, r)
		byIP[ip] = len(list) - 1
		added++
	}
	list = refreshRegistryStatuses(list, now, h)
	saveKnownRegistries(list)
	return added
}

func knownRegistryIPsByStatus(self string, h PeerHealth, includeOffline bool, offlineEveryN, cycle int) (onlineStale, offline []string) {
	self = normalizeYggIP(self)
	now := time.Now().UTC()
	list := refreshRegistryStatuses(loadKnownRegistries(), now, h)
	saveKnownRegistries(list)
	seen := map[string]bool{}
	for _, r := range list {
		ip := normalizeYggIP(r.YggIP)
		if ip == "" || ip == self || seen[ip] {
			continue
		}
		seen[ip] = true
		switch r.Status {
		case StatusOnline, StatusStale:
			onlineStale = append(onlineStale, ip)
		case StatusOffline:
			if includeOffline && (offlineEveryN <= 1 || cycle%offlineEveryN == 0) {
				offline = append(offline, ip)
			}
		}
	}
	return onlineStale, offline
}

func listRegistryPeerInfos(self string, h PeerHealth) []RegistryPeerInfo {
	self = normalizeYggIP(self)
	now := time.Now().UTC()
	list := refreshRegistryStatuses(loadKnownRegistries(), now, h)
	saveKnownRegistries(list)
	out := make([]RegistryPeerInfo, 0, len(list))
	for _, r := range list {
		ip := normalizeYggIP(r.YggIP)
		if ip == "" {
			continue
		}
		info := RegistryPeerInfo{
			YggIP:    ip,
			PeerID:   r.PeerID,
			Role:     RoleRegistry,
			Source:   r.Source,
			Status:   r.Status,
			Failures: r.Failures,
		}
		if !r.LastSeen.IsZero() {
			info.LastSeen = r.LastSeen.UTC().Format(time.RFC3339)
		}
		if !r.LastOK.IsZero() {
			info.LastOK = r.LastOK.UTC().Format(time.RFC3339)
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].YggIP < out[j].YggIP
	})
	return out
}

func registryStatusCounts(self string, h PeerHealth) (online, stale, offline int) {
	for _, p := range listRegistryPeerInfos(self, h) {
		switch p.Status {
		case StatusOnline:
			online++
		case StatusStale:
			stale++
		default:
			offline++
		}
	}
	return online, stale, offline
}

func exportRegistriesForExchange(self string, h PeerHealth) []knownRegistry {
	self = normalizeYggIP(self)
	now := time.Now().UTC()
	list := refreshRegistryStatuses(loadKnownRegistries(), now, h)
	out := make([]knownRegistry, 0, len(list)+1)
	if self != "" {
		out = append(out, knownRegistry{
			YggIP:    self,
			Role:     RoleRegistry,
			Source:   "self",
			LastSeen: now,
			LastOK:   now,
			Status:   StatusOnline,
		})
	}
	for _, r := range list {
		ip := normalizeYggIP(r.YggIP)
		if ip == "" || ip == self {
			continue
		}
		out = append(out, r)
	}
	return out
}
