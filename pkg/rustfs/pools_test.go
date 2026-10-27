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
// 1.0.3 for a single-pool deployment (the compose acceptance environment).
const poolsListPayload = `[{"id":0,"cmdline":"/data","lastUpdate":"2026-10-19T06:31:51.126435444Z",` +
	`"totalSize":63728975872,"currentSize":53833203712,"usedSize":9895772160,` +
	`"used":0.15527900809006742,"status":"active","decommissionStatus":"none",` +
	`"rebalanceStatus":"none","decommissionInfo":null}]`

func TestListPools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/pools/list" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(poolsListPayload))
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
	if pools[0].Name != "/data" {
		t.Errorf("expected the pool command line as its name, got %q", pools[0].Name)
	}
	if pools[0].Status != "active" {
		t.Errorf("expected active, got %q", pools[0].Status)
	}
}
