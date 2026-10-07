package rustfs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTierStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/rustfs/admin/v3/tier-stats") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contractVersion":2,"inventory":{"status":"accounted"},"activity":{"status":"complete","nodesReporting":1,"nodesExpected":1,"unavailableNodes":[]},"tiers":[{"name":"WARM","inventory":{"totalSize":15,"numVersions":3,"numObjects":1},"transitionsLast24h":{"totalSize":0,"numVersions":0,"numObjects":0}},{"name":"ARCHIVE","inventory":{"totalSize":9,"numVersions":1,"numObjects":1},"transitionsLast24h":{"totalSize":0,"numVersions":0,"numObjects":0}}]}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	stats, err := client.TierStats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 tiers, got %d: %v", len(stats), stats)
	}
	warm, ok := stats["WARM"]
	if !ok {
		t.Fatalf("expected WARM tier, got %v", stats)
	}
	if warm.TotalSize != 15 || warm.NumVersions != 3 || warm.NumObjects != 1 {
		t.Errorf("unexpected WARM stats: %+v", warm)
	}
	archive, ok := stats["ARCHIVE"]
	if !ok {
		t.Fatalf("expected ARCHIVE tier, got %v", stats)
	}
	if archive.TotalSize != 9 || archive.NumVersions != 1 || archive.NumObjects != 1 {
		t.Errorf("unexpected ARCHIVE stats: %+v", archive)
	}
}

func TestTierStatsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contractVersion":2,"inventory":{"status":"not-accounted"},"activity":{"status":"complete","nodesReporting":1,"nodesExpected":1,"unavailableNodes":[]},"tiers":[]}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	stats, err := client.TierStats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("expected no tiers, got %v", stats)
	}
}

func TestTierStatsFallsBackToActivity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contractVersion":2,"inventory":{"status":"not-accounted"},"activity":{"status":"complete","nodesReporting":1,"nodesExpected":1,"unavailableNodes":[]},"tiers":[{"name":"COLD","transitionsLast24h":{"totalSize":42,"numVersions":2,"numObjects":1}}]}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	stats, err := client.TierStats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cold, ok := stats["COLD"]
	if !ok {
		t.Fatalf("expected COLD tier, got %v", stats)
	}
	if cold.TotalSize != 42 || cold.NumVersions != 2 || cold.NumObjects != 1 {
		t.Errorf("unexpected COLD stats: %+v", cold)
	}
}
