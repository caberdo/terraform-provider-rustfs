// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

const notImplementedBody = `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented.</Message></Error>`

func TestParseServerVersion(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		want       string
		wantPre    string
		wantFailed bool
	}{
		{name: "plain release", raw: "1.0.3", want: "1.0.3"},
		{name: "tag reference", raw: "refs/tags/1.0.1", want: "1.0.1"},
		{name: "build timestamp prefix", raw: "2026-09-01T17:35:49Z@1.0.0-rc.5", want: "1.0.0-rc.5", wantPre: "rc.5"},
		{name: "v prefix", raw: "v1.0.2", want: "1.0.2"},
		{name: "build metadata", raw: "1.0.3+build.7", want: "1.0.3"},
		{name: "whitespace", raw: "  1.0.2\n", want: "1.0.2"},
		{name: "empty", raw: "", wantFailed: true},
		{name: "not a version", raw: "latest", wantFailed: true},
		{name: "major only", raw: "1", wantFailed: true},
		{name: "two segments", raw: "1.0", wantFailed: true},
		{name: "non numeric", raw: "1.x.0", wantFailed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, err := rustfs.ParseServerVersion(tt.raw)
			if tt.wantFailed {
				if err == nil {
					t.Fatalf("expected an error for %q, got version %s", tt.raw, version)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.raw, err)
			}
			if got := version.String(); got != tt.want {
				t.Errorf("version %q: got %s, want %s", tt.raw, got, tt.want)
			}
			if version.Prerelease != tt.wantPre {
				t.Errorf("version %q: got prerelease %q, want %q", tt.raw, version.Prerelease, tt.wantPre)
			}
			if version.Raw != strings.TrimSpace(tt.raw) {
				t.Errorf("version %q: raw is %q", tt.raw, version.Raw)
			}
		})
	}
}

func TestVersionCompare(t *testing.T) {
	parse := func(t *testing.T, raw string) rustfs.Version {
		t.Helper()
		version, err := rustfs.ParseServerVersion(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		return version
	}

	ordered := []string{"1.0.0-rc.5", "1.0.0", "1.0.1", "1.0.2", "1.0.3", "1.1.0"}
	for i := range len(ordered) - 1 {
		older, newer := parse(t, ordered[i]), parse(t, ordered[i+1])
		if cmp := older.Compare(newer); cmp != -1 {
			t.Errorf("%s.Compare(%s) = %d, want -1", ordered[i], ordered[i+1], cmp)
		}
		if cmp := newer.Compare(older); cmp != 1 {
			t.Errorf("%s.Compare(%s) = %d, want 1", ordered[i+1], ordered[i], cmp)
		}
	}

	if !parse(t, "1.0.2").AtLeast(parse(t, "1.0.2")) {
		t.Error("1.0.2 should be at least 1.0.2")
	}
	if !parse(t, "1.0.3").AtLeast(parse(t, "1.0.2")) {
		t.Error("1.0.3 should be at least 1.0.2")
	}
	if parse(t, "1.0.1").AtLeast(parse(t, "1.0.2")) {
		t.Error("1.0.1 should not be at least 1.0.2")
	}
	if parse(t, "1.0.2-rc.1").AtLeast(parse(t, "1.0.2")) {
		t.Error("a 1.0.2 prerelease should not satisfy a 1.0.2 requirement")
	}
}

func TestMetricsFeatureRoute(t *testing.T) {
	if got, want := rustfs.FeatureMetrics.Route(), "GET /rustfs/admin/v3/metrics"; got != want {
		t.Errorf("route = %q, want %q", got, want)
	}
}

func TestUnregisteredFeatureIsProgrammingError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Feature.Route on an unregistered feature must panic instead of returning an empty route")
		}
	}()

	_ = rustfs.Feature("not-registered").Route()
}

// newAdminTestServer wires an httptest server that answers the metrics and
// healthinfo routes with the supplied handlers.
func newAdminTestServer(t *testing.T, metrics, health http.HandlerFunc) rustfs.RustfsAdmin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/rustfs/admin/v3/metrics"):
			metrics(w, r)
		case strings.HasSuffix(r.URL.Path, "/rustfs/admin/v3/healthinfo"):
			health(w, r)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	return rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = io.WriteString(w, notImplementedBody)
}

func versionResponse(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"`+version+`"}`)
	}
}

func TestGetMetricsNotImplementedBecomesUnsupportedFeature(t *testing.T) {
	client := newAdminTestServer(t, notImplemented, versionResponse("refs/tags/1.0.3"))

	raw, err := client.GetMetrics(context.Background())
	if raw != "" {
		t.Errorf("expected an empty metrics body, got %q", raw)
	}
	if err == nil {
		t.Fatal("expected an error")
	}

	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureMetrics {
		t.Errorf("feature = %q, want %q", unsupported.Feature, rustfs.FeatureMetrics)
	}
	if unsupported.Route != "GET /rustfs/admin/v3/metrics" {
		t.Errorf("route = %q", unsupported.Route)
	}
	if unsupported.ServerVersion != "1.0.3" {
		t.Errorf("server version = %q, want 1.0.3", unsupported.ServerVersion)
	}
	if unsupported.MinVersion != nil {
		t.Errorf("expected no minimum version, got %s", unsupported.MinVersion)
	}

	message := err.Error()
	for _, want := range []string{"RustFS 1.0.3", "does not implement", "GET /rustfs/admin/v3/metrics", "metrics"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "<Error>") {
		t.Errorf("message leaks the raw error payload: %q", message)
	}

	// The underlying API error stays reachable for callers that need the status.
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotImplemented {
		t.Errorf("expected the wrapped APIError with status 501, got %v", err)
	}
}

func TestGetMetricsCapabilityGapWithoutServerVersion(t *testing.T) {
	client := newAdminTestServer(t, notImplemented, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := client.GetMetrics(context.Background())
	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.ServerVersion != "" {
		t.Errorf("server version = %q, want empty", unsupported.ServerVersion)
	}
	if message := err.Error(); !strings.Contains(message, "the RustFS server does not implement") {
		t.Errorf("unexpected message: %q", message)
	}
}

func TestGetMetricsStatusCodesThatSignalMissingRoute(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := newAdminTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `<Error><Code>Whatever</Code></Error>`)
			}, versionResponse("refs/tags/1.0.2"))

			_, err := client.GetMetrics(context.Background())
			if !rustfs.IsUnsupportedFeature(err) {
				t.Fatalf("status %d: expected *UnsupportedFeatureError, got %T: %v", status, err, err)
			}
		})
	}
}

func TestGetMetricsServerErrorStaysAnAPIError(t *testing.T) {
	client := newAdminTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `<Error><Code>InternalError</Code><Message>boom</Message></Error>`)
	}, versionResponse("refs/tags/1.0.3"))

	_, err := client.GetMetrics(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("a 500 must not be reported as a capability gap: %v", err)
	}
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "InternalError" {
		t.Errorf("code = %q, want InternalError", apiErr.Code)
	}
}

func TestServerVersion(t *testing.T) {
	client := newAdminTestServer(t, notImplemented, versionResponse("2026-09-01T17:35:49Z@1.0.0-rc.5"))

	version, raw, err := client.ServerVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw != "2026-09-01T17:35:49Z@1.0.0-rc.5" {
		t.Errorf("raw = %q", raw)
	}
	if got := version.String(); got != "1.0.0-rc.5" {
		t.Errorf("version = %s", got)
	}
}

func TestServerVersionUnparseableKeepsTheRawString(t *testing.T) {
	client := newAdminTestServer(t, notImplemented, versionResponse("nightly"))

	_, raw, err := client.ServerVersion(context.Background())
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if raw != "nightly" {
		t.Errorf("raw = %q, want nightly", raw)
	}
}
