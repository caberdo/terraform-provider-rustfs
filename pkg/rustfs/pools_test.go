// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The payload below is what GET /rustfs/admin/v3/pools/list returns on RustFS
// 1.0.3 for a single-pool deployment (the compose acceptance environment). It
// predates the 1.0.5 diagnostics, so none of the new keys are present.
const poolsListPayload = `[{"id":0,"cmdline":"/data","lastUpdate":"2026-10-19T06:31:51.126435444Z",` +
	`"totalSize":63728975872,"currentSize":53833203712,"usedSize":9895772160,` +
	`"used":0.15527900809006742,"status":"active","decommissionStatus":"none",` +
	`"rebalanceStatus":"none","decommissionInfo":null}]`

// poolsListPayload105 is a live 1.0.5 answer for the same deployment, rendered
// with the five diagnostic keys rustfs/rustfs#8995 added.
const poolsListPayload105 = `[{"id":0,"cmdline":"/data","lastUpdate":"2026-11-11T08:12:05.210883873Z",` +
	`"totalSize":63728975872,"currentSize":47960670208,"usedSize":15768305664,` +
	`"used":0.2474275703986006,"status":"active","decommissionStatus":"none",` +
	`"rebalanceStatus":"none","decommissionInfo":null,"availability":"online",` +
	`"poolMetadataStatus":"fresh","rebalanceMetadataStatus":"fresh","capacityStatus":"live",` +
	`"observedAt":"2026-11-11T08:19:27.821896967Z"}]`

// listPoolsFromServer serves payload on the pools/list route and returns the
// decoded pools.
func listPoolsFromServer(t *testing.T, payload string) []PoolInfo {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/pools/list" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	pools, err := client.ListPools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}
	return pools
}

func TestListPools(t *testing.T) {
	pools := listPoolsFromServer(t, poolsListPayload)

	if pools[0].Name != "/data" {
		t.Errorf("expected the pool command line as its name, got %q", pools[0].Name)
	}
	if pools[0].Status != "active" {
		t.Errorf("expected active, got %q", pools[0].Status)
	}
}

func TestListPoolsDiagnostics(t *testing.T) {
	t.Run("1.0.5 reports the diagnostics", func(t *testing.T) {
		pool := listPoolsFromServer(t, poolsListPayload105)[0]

		fields := []struct {
			name string
			got  *string
			want string
		}{
			{"availability", pool.Availability, "online"},
			{"capacityStatus", pool.CapacityStatus, "live"},
			{"poolMetadataStatus", pool.PoolMetadataStatus, "fresh"},
			{"rebalanceMetadataStatus", pool.RebalanceMetadataStatus, "fresh"},
			{"observedAt", pool.ObservedAt, "2026-11-11T08:19:27.821896967Z"},
		}
		for _, field := range fields {
			if field.got == nil {
				t.Errorf("%s: expected %q, got nil", field.name, field.want)
				continue
			}
			if *field.got != field.want {
				t.Errorf("%s: expected %q, got %q", field.name, field.want, *field.got)
			}
		}
	})

	t.Run("pre-1.0.5 leaves the diagnostics null", func(t *testing.T) {
		pool := listPoolsFromServer(t, poolsListPayload)[0]

		fields := []struct {
			name string
			got  *string
		}{
			{"availability", pool.Availability},
			{"capacityStatus", pool.CapacityStatus},
			{"poolMetadataStatus", pool.PoolMetadataStatus},
			{"rebalanceMetadataStatus", pool.RebalanceMetadataStatus},
			{"observedAt", pool.ObservedAt},
		}
		for _, field := range fields {
			if field.got != nil {
				t.Errorf("%s: an absent key must decode to nil, got %q", field.name, *field.got)
			}
		}
		// The keys the older payload does carry keep decoding as before.
		if pool.Name != "/data" || pool.Status != "active" {
			t.Errorf("unexpected name/status: %q/%q", pool.Name, pool.Status)
		}
	})
}

func TestListPoolsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"InternalError","message":"pool listing is unavailable"}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	pools, err := client.ListPools(context.Background())
	if err == nil {
		t.Fatal("expected the server failure to surface")
	}
	if pools != nil {
		t.Errorf("expected no pools on failure, got %v", pools)
	}
}
