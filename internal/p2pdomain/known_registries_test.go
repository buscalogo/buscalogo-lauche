package p2pdomain

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeRegistryStatus(t *testing.T) {
	h := PeerHealth{
		StaleAfter:   5 * time.Minute,
		OfflineAfter: time.Hour,
		PruneAfter:   24 * time.Hour,
	}
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	online := knownRegistry{LastOK: now.Add(-2 * time.Minute)}
	if got := computeRegistryStatus(online, now, h); got != StatusOnline {
		t.Fatalf("online: %s", got)
	}
	stale := knownRegistry{LastOK: now.Add(-20 * time.Minute)}
	if got := computeRegistryStatus(stale, now, h); got != StatusStale {
		t.Fatalf("stale: %s", got)
	}
	offline := knownRegistry{LastOK: now.Add(-2 * time.Hour)}
	if got := computeRegistryStatus(offline, now, h); got != StatusOffline {
		t.Fatalf("offline: %s", got)
	}
}

func TestMergeAndPruneRegistries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BUSCALOGO_HOME", dir)
	// paths.Data uses BUSCALOGO_HOME/data — ensure layout
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	h := DefaultPeerHealth()
	now := time.Now().UTC()
	remote := []knownRegistry{
		{YggIP: "200::1", Role: RoleRegistry, LastSeen: now, LastOK: now, Status: StatusOnline},
		{YggIP: "200::2", Role: RoleRegistry, LastSeen: now.Add(-2 * time.Hour), LastOK: now.Add(-2 * time.Hour)},
	}
	n := mergeRegistryPeers(remote, SourceExchange, "200::9", h)
	if n < 1 {
		t.Fatalf("expected adds, got %d", n)
	}
	rememberRegistry("200::1", "peer1", SourceDiscover, true, h)
	list := listRegistryPeerInfos("200::9", h)
	found := false
	for _, p := range list {
		if p.YggIP == "200:0:0:0:0:0:0:1" || p.YggIP == "200::1" {
			found = true
			if p.Status != StatusOnline {
				t.Fatalf("status=%s", p.Status)
			}
		}
	}
	if !found {
		t.Fatalf("missing 200::1 in %#v", list)
	}

	// Prune very old offline
	h.PruneAfter = time.Minute
	old := knownRegistry{
		YggIP:    "200::3",
		Role:     RoleRegistry,
		LastSeen: now.Add(-2 * time.Hour),
		LastOK:   now.Add(-2 * time.Hour),
		Status:   StatusOffline,
	}
	_ = mergeRegistryPeers([]knownRegistry{old}, SourceExchange, "200::9", h)
	pruned := refreshRegistryStatuses(loadKnownRegistries(), now, h)
	for _, r := range pruned {
		if normalizeYggIP(r.YggIP) == normalizeYggIP("200::3") {
			t.Fatal("expected prune of 200::3")
		}
	}
}

func TestFetchBootstrapRegistriesParse(t *testing.T) {
	// Empty / off
	ips, err := FetchBootstrapRegistries(t.Context(), "off")
	if err != nil || ips != nil {
		t.Fatalf("off: %v %v", ips, err)
	}
}
