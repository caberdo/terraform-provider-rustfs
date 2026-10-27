// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

func TestGetMetrics(t *testing.T) {
	body := `{"errors":[],"hosts":[":9000"],"aggregated":{"scanner":{"collected":"2026-09-08T12:26:53Z","current_cycle":0,"current_cycle_active":false}},"perf":{"drive":{"read_throughput":"123","write_throughput":"456"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/rustfs/admin/v3/metrics") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})

	raw, err := client.GetMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(raw, "read_throughput") {
		t.Errorf("expected metrics content, got: %s", raw)
	}
	if !strings.Contains(raw, "current_cycle") {
		t.Errorf("expected scanner metrics content, got: %s", raw)
	}
}
