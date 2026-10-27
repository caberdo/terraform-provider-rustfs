// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

const objectDataCacheStatsPayload = `{"mode":"memory","disabled":false,"entries":12,"lookups":100,` +
	`"hits":70,"fills":30,"invalidations":4,"inflight_fills":1,"singleflight_joins":5,` +
	`"memory_pressure_events":2}`

func newObjectDataCacheTestClient(t *testing.T, handler http.HandlerFunc) rustfs.RustfsAdmin {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "unit-test-access-key",
		AccessSecret: "unit-test-secret-key",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
}

func unsupportedObjectDataCacheRoute(t *testing.T, route string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, route):
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, notImplementedBody)
		case strings.HasSuffix(r.URL.Path, "/rustfs/admin/v3/healthinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.3"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func TestGetObjectDataCacheStats(t *testing.T) {
	var method, requestPath string
	client := newObjectDataCacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, requestPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, objectDataCacheStatsPayload)
	})

	stats, err := client.GetObjectDataCacheStats(context.Background())
	if err != nil {
		t.Fatalf("read object data cache stats: %v", err)
	}

	if method != http.MethodGet {
		t.Errorf("method = %q, want GET", method)
	}
	if want := "/rustfs/admin/v3/object-data-cache/stats"; requestPath != want {
		t.Errorf("path = %q, want %q", requestPath, want)
	}
	if stats.Mode != "memory" || stats.Disabled {
		t.Errorf("mode = %q disabled=%v", stats.Mode, stats.Disabled)
	}
	if stats.Entries != 12 || stats.Lookups != 100 || stats.Hits != 70 || stats.Fills != 30 {
		t.Errorf("counters = %+v", stats)
	}
	if stats.Invalidations != 4 || stats.InflightFills != 1 || stats.SingleflightJoins != 5 {
		t.Errorf("counters = %+v", stats)
	}
	if stats.MemoryPressureEvents != 2 {
		t.Errorf("memory pressure events = %d", stats.MemoryPressureEvents)
	}
	if stats.RawJSON != objectDataCacheStatsPayload {
		t.Errorf("raw JSON was not kept verbatim")
	}
}

func TestGetObjectDataCacheStatsDisabled(t *testing.T) {
	const body = `{"mode":"disabled","disabled":true,"entries":0,"lookups":0,"hits":0,"fills":0,` +
		`"invalidations":0,"inflight_fills":0,"singleflight_joins":0,"memory_pressure_events":0}`

	client := newObjectDataCacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})

	stats, err := client.GetObjectDataCacheStats(context.Background())
	if err != nil {
		t.Fatalf("read object data cache stats: %v", err)
	}
	if !stats.Disabled || stats.Mode != "disabled" {
		t.Errorf("mode = %q disabled=%v", stats.Mode, stats.Disabled)
	}
}

func TestObjectDataCacheMalformedBody(t *testing.T) {
	tests := []struct {
		name string
		call func(rustfs.RustfsAdmin) error
	}{
		{
			name: "stats",
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.GetObjectDataCacheStats(context.Background())
				return err
			},
		},
		{
			name: "flush",
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.FlushObjectDataCache(context.Background(), "", "")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newObjectDataCacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `not-json`)
			})

			if err := tc.call(client); err == nil {
				t.Fatal("expected a decode error")
			}
		})
	}
}

func TestFlushObjectDataCache(t *testing.T) {
	tests := []struct {
		name        string
		bucket      string
		object      string
		wantQuery   string
		wantScope   string
		wantRemoved int
	}{
		{name: "whole cache", wantQuery: "", wantScope: "all", wantRemoved: 0},
		{name: "one bucket", bucket: "logs", wantQuery: "bucket=logs", wantScope: "bucket", wantRemoved: 3},
		{name: "one object", bucket: "logs", object: "2026/10/19/a.log", wantQuery: "bucket=logs&object=2026%2F10%2F19%2Fa.log", wantScope: "object", wantRemoved: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var method, requestPath, query string
			client := newObjectDataCacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				method, requestPath, query = r.Method, r.URL.Path, r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"scope":"`+tc.wantScope+`","bucket":null,"object":null,`+
					`"outcome":"removed","removed_keys":`+strconv.Itoa(tc.wantRemoved)+`}`)
			})

			result, err := client.FlushObjectDataCache(context.Background(), tc.bucket, tc.object)
			if err != nil {
				t.Fatalf("flush object data cache: %v", err)
			}

			if method != http.MethodPost {
				t.Errorf("method = %q, want POST", method)
			}
			if want := "/rustfs/admin/v3/object-data-cache/flush"; requestPath != want {
				t.Errorf("path = %q, want %q", requestPath, want)
			}
			if query != tc.wantQuery {
				t.Errorf("query = %q, want %q", query, tc.wantQuery)
			}
			if result.Scope != tc.wantScope || result.Outcome != "removed" {
				t.Errorf("result = %+v", result)
			}
			if result.RemovedKeys != uint64(tc.wantRemoved) {
				t.Errorf("removed keys = %d, want %d", result.RemovedKeys, tc.wantRemoved)
			}
		})
	}
}

func TestFlushObjectDataCacheRejectsObjectWithoutBucket(t *testing.T) {
	client := newObjectDataCacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})

	if _, err := client.FlushObjectDataCache(context.Background(), "", "a.log"); err == nil {
		t.Fatal("expected an error for an object flush without a bucket")
	}
}

func TestObjectDataCacheNotImplementedBecomesUnsupportedFeature(t *testing.T) {
	tests := []struct {
		name    string
		route   string
		feature rustfs.Feature
		call    func(rustfs.RustfsAdmin) error
	}{
		{
			name:    "stats",
			route:   "/rustfs/admin/v3/object-data-cache/stats",
			feature: rustfs.FeatureObjectDataCacheStats,
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.GetObjectDataCacheStats(context.Background())
				return err
			},
		},
		{
			name:    "flush",
			route:   "/rustfs/admin/v3/object-data-cache/flush",
			feature: rustfs.FeatureObjectDataCacheFlush,
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.FlushObjectDataCache(context.Background(), "", "")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newObjectDataCacheTestClient(t, unsupportedObjectDataCacheRoute(t, tc.route))

			err := tc.call(client)
			var unsupported *rustfs.UnsupportedFeatureError
			if !errors.As(err, &unsupported) {
				t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
			}
			if unsupported.Feature != tc.feature {
				t.Errorf("feature = %q, want %q", unsupported.Feature, tc.feature)
			}
			if unsupported.ServerVersion != "1.0.3" {
				t.Errorf("server version = %q, want 1.0.3", unsupported.ServerVersion)
			}
		})
	}
}
