// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequestVersionURLs pins the three URL shapes the client emits: an empty
// Version keeps the historical v3 route, an explicit Version reaches the
// versioned admin base, and DoDirectRequest stays on the host root.
func TestRequestVersionURLs(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)

	client := New(&RustfsAdminConfig{
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
		AccessKey:    "unit-test-access-key",
		AccessSecret: "unit-test-secret",
	})
	ctx := context.Background()

	if resp, err := client.doRequest(ctx, RequestData{Method: http.MethodGet, RelPath: "info"}); err != nil {
		t.Fatalf("v3 request: %v", err)
	} else {
		drainClose(resp)
	}
	if resp, err := client.doRequest(ctx, RequestData{Method: http.MethodGet, RelPath: "runtime/capabilities", Version: "v4"}); err != nil {
		t.Fatalf("v4 request: %v", err)
	} else {
		drainClose(resp)
	}
	if resp, err := client.DoDirectRequest(ctx, RequestData{Method: http.MethodGet, RelPath: "iceberg/v1/buckets/example"}); err != nil {
		t.Fatalf("direct request: %v", err)
	} else {
		drainClose(resp)
	}

	want := []string{
		"/rustfs/admin/v3/info",
		"/rustfs/admin/v4/runtime/capabilities",
		"/iceberg/v1/buckets/example",
	}
	if len(paths) != len(want) {
		t.Fatalf("requested paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("request %d reached %q, want %q", i, paths[i], want[i])
		}
	}
}

// TestNewStoresRootAndAdminBase keeps the version out of the stored base URLs;
// adminURL is the only place a version is applied.
func TestNewStoresRootAndAdminBase(t *testing.T) {
	client := New(&RustfsAdminConfig{Endpoint: "rustfs.example.com:9000"})

	if client.rootURL != "http://rustfs.example.com:9000" {
		t.Errorf("rootURL = %q", client.rootURL)
	}
	if client.adminBaseURL != "http://rustfs.example.com:9000/rustfs/admin" {
		t.Errorf("adminBaseURL = %q", client.adminBaseURL)
	}
	if got, err := client.adminURL(context.Background(), ""); err != nil {
		t.Errorf("adminURL(\"\") error: %v", err)
	} else if got != "http://rustfs.example.com:9000/rustfs/admin/v3" {
		t.Errorf("adminURL(\"\") = %q", got)
	}
	if got, err := client.adminURL(context.Background(), "v4"); err != nil {
		t.Errorf("adminURL(\"v4\") error: %v", err)
	} else if got != "http://rustfs.example.com:9000/rustfs/admin/v4" {
		t.Errorf("adminURL(\"v4\") = %q", got)
	}
	for _, version := range []string{"v5", "V4", "4", "v4/extra"} {
		if _, err := client.adminURL(context.Background(), version); err == nil {
			t.Errorf("adminURL(%q) accepted an unsupported version", version)
		}
	}
}

// TestUnsupportedRequestVersionRejected keeps an accidental version string from
// silently producing a request against a namespace the provider does not model.
func TestUnsupportedRequestVersionRejected(t *testing.T) {
	client := New(&RustfsAdminConfig{Endpoint: "rustfs.example.com:9000"})

	if _, err := client.createRequest(context.Background(), RequestData{Method: http.MethodGet, RelPath: "info", Version: "v5"}); err == nil {
		t.Fatal("expected createRequest to reject an unsupported version")
	} else if !strings.Contains(err.Error(), "unsupported admin API version") {
		t.Errorf("error = %q", err)
	}
}

// TestDoDirectRequestRejectsVersion keeps the direct path honest: it resolves
// against the host root, so an admin API version would be silently ignored.
func TestDoDirectRequestRejectsVersion(t *testing.T) {
	client := New(&RustfsAdminConfig{Endpoint: "rustfs.example.com:9000"})

	if resp, err := client.DoDirectRequest(context.Background(), RequestData{Method: http.MethodGet, RelPath: "x", Version: "v4"}); err == nil {
		drainClose(resp)
		t.Fatal("expected DoDirectRequest to reject a version")
	} else if !strings.Contains(err.Error(), "does not take an admin API version") {
		t.Errorf("error = %q", err)
	}
}
