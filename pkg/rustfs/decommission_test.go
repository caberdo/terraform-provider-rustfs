// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// newDecommissionTestClient builds an admin client for the mocked server. The
// mocked API never authenticates, so only non-empty credentials are needed;
// they come from the environment when set, with a generated fallback so no
// credential literal lives in the source.
func newDecommissionTestClient(server *httptest.Server) RustfsAdmin {
	return New(&RustfsAdminConfig{
		Endpoint:     server.Listener.Addr().String(),
		AccessKey:    decommissionTestCredential("RUSTFS_ACCESS_KEY", 'a'),
		AccessSecret: decommissionTestCredential("RUSTFS_SECRET", 's'),
	})
}

func decommissionTestCredential(env string, fallback rune) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return strings.Repeat(string(fallback), 16)
}

// The payloads below are copied from GET /rustfs/admin/v3/decommission/status
// on RustFS 1.0.3 with two pools, while pool 1 is draining.
const decommissionStatusPayload = `{"pools":[` +
	`{"id":0,"cmdline":"/data/p1{1...2}","status":"none","poolStatus":"active","decommissionInfo":null},` +
	`{"id":1,"cmdline":"/data/p2{1...2}","status":"running","poolStatus":"decommissioning",` +
	`"decommissionInfo":{"startTime":"2026-10-20T12:52:24.324482583Z","startSize":50827755520,` +
	`"totalSize":63728975872,"currentSize":50827755520,"complete":false,"failed":false,` +
	`"canceled":false,"queued":false,"queuedBuckets":["drainbucket"],` +
	`"decommissionedBuckets":[],"bucket":".rustfs.sys","prefix":"ilm",` +
	`"object":"config/config.json","stage":"source_cleanup","objectsDecommissioned":1,` +
	`"objectsDecommissionedFailed":0,"bytesDecommissioned":107,` +
	`"bytesDecommissionedFailed":0,"waitingReason":null}}]}`

func TestDecommissionStatuses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/decommission/status" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(decommissionStatusPayload))
	}))
	defer server.Close()

	client := newDecommissionTestClient(server)

	statuses, err := client.DecommissionStatuses(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(statuses))
	}
	if statuses[0].Cmdline != "/data/p1{1...2}" || statuses[0].Status != "none" {
		t.Errorf("unexpected first pool: %+v", statuses[0])
	}
	draining := statuses[1]
	if draining.Status != "running" || draining.PoolStatus != "decommissioning" {
		t.Errorf("unexpected drain status: %+v", draining)
	}
	if draining.Info == nil {
		t.Fatal("expected decommission info")
	}
	if got := *draining.Info.StartTime; got != "2026-10-20T12:52:24.324482583Z" {
		t.Errorf("startTime = %q", got)
	}
	if draining.Info.ObjectsDecommissioned != 1 || draining.Info.BytesDecommissioned != 107 {
		t.Errorf("unexpected counters: %+v", *draining.Info)
	}
	if len(draining.Info.QueuedBuckets) != 1 || draining.Info.QueuedBuckets[0] != "drainbucket" {
		t.Errorf("queuedBuckets = %v", draining.Info.QueuedBuckets)
	}
}

func TestDecommissionStatusesRejectsMalformedPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"pools":`))
	}))
	defer server.Close()

	client := newDecommissionTestClient(server)

	if _, err := client.DecommissionStatuses(context.Background()); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestPoolDecommissionMutations(t *testing.T) {
	const pool = "/data/p2{1...2}"

	cases := []struct {
		name    string
		call    func(*RustfsAdmin) error
		relPath string
	}{
		{name: "start", call: func(c *RustfsAdmin) error { return c.StartPoolDecommission(context.Background(), pool) }, relPath: "/rustfs/admin/v3/pools/decommission"},
		{name: "cancel", call: func(c *RustfsAdmin) error { return c.CancelPoolDecommission(context.Background(), pool) }, relPath: "/rustfs/admin/v3/pools/cancel"},
		{name: "clear", call: func(c *RustfsAdmin) error { return c.ClearPoolDecommission(context.Background(), pool) }, relPath: "/rustfs/admin/v3/pools/clear"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}
				if r.URL.Path != tc.relPath {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if got := r.URL.Query().Get("pool"); got != pool {
					t.Errorf("pool query = %q, want %q", got, pool)
				}
				if got := r.URL.Query().Get("by-id"); got != "" {
					t.Errorf("by-id must not be sent, got %q", got)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			client := newDecommissionTestClient(server)

			if err := tc.call(&client); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDecommissionErrorsAreStructured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<Error><Code>InvalidArgument</Code><Message>Failed to start decommission: invalid pool ` + "`/nope`" + `</Message></Error>`))
	}))
	defer server.Close()

	client := newDecommissionTestClient(server)

	err := client.StartPoolDecommission(context.Background(), "/nope")
	if !IsAPIErrorCode(err, "InvalidArgument") {
		t.Fatalf("expected an InvalidArgument APIError, got %T: %v", err, err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", apiErr.StatusCode)
	}
}

func TestDecommissionNotImplementedIsFeatureGated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rustfs/admin/v3/healthinfo":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version":"refs/tags/1.0.3"}`))
		default:
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented</Message></Error>`))
		}
	}))
	defer server.Close()

	client := newDecommissionTestClient(server)

	_, err := client.DecommissionStatuses(context.Background())
	if !IsUnsupportedFeature(err) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T", err)
	}
	if unsupported.Feature != FeaturePoolDecommission {
		t.Errorf("feature = %q, want %q", unsupported.Feature, FeaturePoolDecommission)
	}
	if unsupported.ServerVersion != "1.0.3" {
		t.Errorf("server version = %q, want 1.0.3", unsupported.ServerVersion)
	}
	if unsupported.MinVersion == nil || unsupported.MinVersion.String() != "1.0.1" {
		t.Errorf("minimum version = %v, want 1.0.1", unsupported.MinVersion)
	}
}
