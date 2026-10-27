// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

// scannerStatusPayload mirrors the JSON GET /v3/scanner/status produces on
// RustFS 1.0.x. It is trimmed to the fields the provider surfaces; the server
// sends many more, and the data source keeps the untouched body as status_json.
const scannerStatusPayload = `{
  "enabled": true,
  "disabled_reason": null,
  "freshness": {"state": "fresh", "last_cycle_end_unix_secs": 1760000000, "max_expected_age_seconds": 120, "reason": null},
  "metrics": {
    "current_cycle": 42,
    "current_cycle_active": false,
    "current_scan_mode": "normal",
    "last_cycle_end_unix_secs": 1760000000,
    "last_cycle_result": "success",
    "last_cycle_duration_seconds": 12.5,
    "last_cycle_objects_scanned": 1234,
    "last_cycle_directories_scanned": 56,
    "cycles_completed_at": ["2026-10-19T06:31:51Z", "2026-10-19T06:32:51Z"],
    "failed_cycles": 1,
    "superseded_cycles": 2,
    "usage_freshness": {
      "dirty_pending_buckets": 3,
      "last_publication_state": "published",
      "last_durable_success_unix_secs": 1760000001
    },
    "maintenance_control": {"primary_control": "none"}
  },
  "cycle_schedule": {"effective_interval_seconds": 60, "usage_bootstrap_rebuild_pending": false},
  "cycle_recovery": {"state": "healthy", "reason": null},
  "data_movement_pause": {"paused": false},
  "pause_backlog": {"phase": "idle"}
}`

func newScannerTestClient(t *testing.T, handler http.HandlerFunc) rustfs.RustfsAdmin {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// The httptest server does not verify the signature; these values only
	// label the requests.
	return rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "unit-test-access-key",
		AccessSecret: "unit-test-secret-key",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
}

func unsupportedScannerRoute(t *testing.T, route string) http.HandlerFunc {
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

func TestGetScannerStatus(t *testing.T) {
	var method, requestPath string
	client := newScannerTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, requestPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, scannerStatusPayload)
	})

	status, err := client.GetScannerStatus(context.Background())
	if err != nil {
		t.Fatalf("read scanner status: %v", err)
	}

	if method != http.MethodGet {
		t.Errorf("method = %q, want GET", method)
	}
	if want := "/rustfs/admin/v3/scanner/status"; requestPath != want {
		t.Errorf("path = %q, want %q", requestPath, want)
	}

	if !status.Enabled {
		t.Error("enabled = false, want true")
	}
	if status.DisabledReason != nil {
		t.Errorf("disabled reason = %q, want nil", *status.DisabledReason)
	}
	if status.FreshnessState != "fresh" {
		t.Errorf("freshness state = %q, want fresh", status.FreshnessState)
	}
	if status.CurrentCycle != 42 || status.CurrentCycleActive {
		t.Errorf("cycle = %d active=%v, want 42 inactive", status.CurrentCycle, status.CurrentCycleActive)
	}
	if status.CurrentScanMode != "normal" {
		t.Errorf("scan mode = %q, want normal", status.CurrentScanMode)
	}
	if status.LastCycleEndUnixSecs != 1760000000 {
		t.Errorf("last cycle end = %d", status.LastCycleEndUnixSecs)
	}
	if status.LastCycleResult != "success" || status.LastCycleDurationSeconds != 12.5 {
		t.Errorf("last cycle = %q/%v", status.LastCycleResult, status.LastCycleDurationSeconds)
	}
	if status.LastCycleObjectsScanned != 1234 || status.LastCycleDirectoriesScanned != 56 {
		t.Errorf("last cycle counters = %d/%d", status.LastCycleObjectsScanned, status.LastCycleDirectoriesScanned)
	}
	if status.CyclesCompleted != 2 {
		t.Errorf("cycles completed = %d, want 2", status.CyclesCompleted)
	}
	if status.FailedCycles != 1 || status.SupersededCycles != 2 {
		t.Errorf("failed/superseded = %d/%d", status.FailedCycles, status.SupersededCycles)
	}
	if status.EffectiveCycleIntervalSeconds != 60 || status.UsageBootstrapRebuildPending {
		t.Errorf("schedule = %d pending=%v", status.EffectiveCycleIntervalSeconds, status.UsageBootstrapRebuildPending)
	}
	if status.UsagePublicationState != "published" || status.UsageDirtyPendingBuckets != 3 {
		t.Errorf("usage = %q dirty=%d", status.UsagePublicationState, status.UsageDirtyPendingBuckets)
	}
	if status.UsageLastDurableSuccessUnixSecs != 1760000001 {
		t.Errorf("usage durable success = %d", status.UsageLastDurableSuccessUnixSecs)
	}
	if status.MaintenancePrimaryControl != "none" {
		t.Errorf("maintenance control = %q", status.MaintenancePrimaryControl)
	}
	if status.CycleRecoveryState != "healthy" || status.CycleRecoveryReason != nil {
		t.Errorf("cycle recovery = %q/%v", status.CycleRecoveryState, status.CycleRecoveryReason)
	}
	if status.DataMovementPaused || status.PauseBacklogPhase != "idle" {
		t.Errorf("movement pause = %v/%q", status.DataMovementPaused, status.PauseBacklogPhase)
	}
	if status.RawJSON != scannerStatusPayload {
		t.Errorf("status JSON was not kept verbatim")
	}
}

func TestGetScannerStatusKeepsReasonStrings(t *testing.T) {
	const payload = `{"enabled":false,"disabled_reason":"maintenance",` +
		`"cycle_recovery":{"state":"paused","reason":"operator paused scanning"}}`

	client := newScannerTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	})

	status, err := client.GetScannerStatus(context.Background())
	if err != nil {
		t.Fatalf("read scanner status: %v", err)
	}
	if status.DisabledReason == nil || *status.DisabledReason != "maintenance" {
		t.Errorf("disabled reason = %v, want maintenance", status.DisabledReason)
	}
	if status.CycleRecoveryReason == nil || *status.CycleRecoveryReason != "operator paused scanning" {
		t.Errorf("cycle recovery reason = %v", status.CycleRecoveryReason)
	}
}

func TestIsScannerLeaderLockBusy(t *testing.T) {
	if rustfs.IsScannerLeaderLockBusy(nil) {
		t.Error("nil error reported as busy")
	}
	if rustfs.IsScannerLeaderLockBusy(errors.New("usage publication failed")) {
		t.Error("unrelated error reported as busy")
	}
	if !rustfs.IsScannerLeaderLockBusy(errors.New("scanner leader lock is busy: try later")) {
		t.Error("busy-lock error was not detected")
	}
}

func TestGetScannerStatusMalformedBody(t *testing.T) {
	client := newScannerTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `not-json`)
	})

	if _, err := client.GetScannerStatus(context.Background()); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestScannerResetsMalformedBody(t *testing.T) {
	tests := []struct {
		name string
		call func(rustfs.RustfsAdmin) error
	}{
		{
			name: "cycle-state reset",
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.ResetScannerCycleState(context.Background())
				return err
			},
		},
		{
			name: "usage-state reset",
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.ResetScannerUsageState(context.Background())
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newScannerTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `not-json`)
			})

			if err := tc.call(client); err == nil {
				t.Fatal("expected a decode error")
			}
		})
	}
}

// readErrorBody fails every read while behaving like a closable body.
type readErrorBody struct{}

func (readErrorBody) Read([]byte) (int, error) { return 0, errors.New("transport read failed") }
func (readErrorBody) Close() error             { return nil }

// roundTripFunc adapts a function to an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestScannerAndCacheReadErrors covers the io.ReadAll branch of the scanner and
// object data cache calls by serving an unreadable response body.
func TestScannerAndCacheReadErrors(t *testing.T) {
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "unit-test-access-key",
		AccessSecret: "unit-test-secret-key",
		Endpoint:     "example.invalid:9000",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       readErrorBody{},
			}, nil
		})},
	})

	tests := []struct {
		name string
		call func() error
	}{
		{"GetScannerStatus", func() error { _, err := client.GetScannerStatus(context.Background()); return err }},
		{"ResetScannerCycleState", func() error { _, err := client.ResetScannerCycleState(context.Background()); return err }},
		{"ResetScannerUsageState", func() error { _, err := client.ResetScannerUsageState(context.Background()); return err }},
		{"GetObjectDataCacheStats", func() error { _, err := client.GetObjectDataCacheStats(context.Background()); return err }},
		{"FlushObjectDataCache", func() error { _, err := client.FlushObjectDataCache(context.Background(), "", ""); return err }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || !strings.Contains(err.Error(), "transport read failed") {
				t.Fatalf("expected the transport read error, got %v", err)
			}
		})
	}
}

func TestGetScannerStatusNotImplementedBecomesUnsupportedFeature(t *testing.T) {
	client := newScannerTestClient(t, unsupportedScannerRoute(t, "/rustfs/admin/v3/scanner/status"))

	_, err := client.GetScannerStatus(context.Background())
	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureScannerStatus {
		t.Errorf("feature = %q", unsupported.Feature)
	}
	if unsupported.Route != "GET /rustfs/admin/v3/scanner/status" {
		t.Errorf("route = %q", unsupported.Route)
	}
	if unsupported.ServerVersion != "1.0.3" {
		t.Errorf("server version = %q, want 1.0.3", unsupported.ServerVersion)
	}
	if unsupported.MinVersion != nil {
		t.Errorf("expected no minimum version, got %s", unsupported.MinVersion)
	}
}

func TestResetScannerCycleState(t *testing.T) {
	var method, requestPath string
	var body map[string]string

	client := newScannerTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, requestPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"reset","mode":"full-rescan"}`)
	})

	result, err := client.ResetScannerCycleState(context.Background())
	if err != nil {
		t.Fatalf("reset cycle state: %v", err)
	}

	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}
	if want := "/rustfs/admin/v3/scanner/cycle-state/reset"; requestPath != want {
		t.Errorf("path = %q, want %q", requestPath, want)
	}
	if len(body) != 1 || body["mode"] != "full-rescan" {
		t.Errorf("body = %v, want {\"mode\":\"full-rescan\"}", body)
	}
	if result.Status != "reset" || result.Mode != "full-rescan" {
		t.Errorf("result = %+v", result)
	}
	if result.RawJSON != `{"status":"reset","mode":"full-rescan"}` {
		t.Errorf("raw JSON = %q", result.RawJSON)
	}
}

func TestResetScannerUsageState(t *testing.T) {
	var method, requestPath string
	var body map[string]string

	const response = `{"status":"reset","mode":"full-rebuild","usage_state":"bootstrap-pending",` +
		`"leader_epoch":9,"next_cycle":42,"reset_paths":["buckets/.usage.v2.json","buckets/.usage.json"]}`

	client := newScannerTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, requestPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	})

	result, err := client.ResetScannerUsageState(context.Background())
	if err != nil {
		t.Fatalf("reset usage state: %v", err)
	}

	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}
	if want := "/rustfs/admin/v3/scanner/usage-state/reset"; requestPath != want {
		t.Errorf("path = %q, want %q", requestPath, want)
	}
	if len(body) != 1 || body["mode"] != "full-rebuild" {
		t.Errorf("body = %v, want {\"mode\":\"full-rebuild\"}", body)
	}
	if result.Status != "reset" || result.Mode != "full-rebuild" || result.UsageState != "bootstrap-pending" {
		t.Errorf("result = %+v", result)
	}
	if result.LeaderEpoch != 9 || result.NextCycle != 42 {
		t.Errorf("epoch/cycle = %d/%d", result.LeaderEpoch, result.NextCycle)
	}
	if len(result.ResetPaths) != 2 || result.ResetPaths[0] != "buckets/.usage.v2.json" {
		t.Errorf("reset paths = %v", result.ResetPaths)
	}
	if result.RawJSON != response {
		t.Errorf("raw JSON was not kept verbatim")
	}
}

// TestScannerResetsPropagateServerErrors covers the request-error branch of
// both reset routes: a denied or failed call keeps the server's error so the
// resource can report it, instead of looking like an unsupported route.
func TestScannerResetsPropagateServerErrors(t *testing.T) {
	tests := []struct {
		name string
		call func(rustfs.RustfsAdmin) error
	}{
		{
			name: "cycle-state reset",
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.ResetScannerCycleState(context.Background())
				return err
			},
		},
		{
			name: "usage-state reset",
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.ResetScannerUsageState(context.Background())
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newScannerTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`)
			})
			err := tc.call(client)
			if !rustfs.IsAPIErrorCode(err, "AccessDenied") {
				t.Fatalf("error = %v, want the server's AccessDenied", err)
			}
		})
	}
}

func TestScannerResetNotImplementedBecomesUnsupportedFeature(t *testing.T) {
	tests := []struct {
		name    string
		route   string
		feature rustfs.Feature
		call    func(rustfs.RustfsAdmin) error
	}{
		{
			name:    "cycle-state reset",
			route:   "/rustfs/admin/v3/scanner/cycle-state/reset",
			feature: rustfs.FeatureScannerCycleStateReset,
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.ResetScannerCycleState(context.Background())
				return err
			},
		},
		{
			name:    "usage-state reset",
			route:   "/rustfs/admin/v3/scanner/usage-state/reset",
			feature: rustfs.FeatureScannerUsageStateReset,
			call: func(client rustfs.RustfsAdmin) error {
				_, err := client.ResetScannerUsageState(context.Background())
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newScannerTestClient(t, unsupportedScannerRoute(t, tc.route))

			err := tc.call(client)
			var unsupported *rustfs.UnsupportedFeatureError
			if !errors.As(err, &unsupported) {
				t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
			}
			if unsupported.Feature != tc.feature {
				t.Errorf("feature = %q, want %q", unsupported.Feature, tc.feature)
			}
			if !strings.Contains(unsupported.Route, tc.route) {
				t.Errorf("route = %q, want it to contain %q", unsupported.Route, tc.route)
			}
			if unsupported.ServerVersion != "1.0.3" {
				t.Errorf("server version = %q, want 1.0.3", unsupported.ServerVersion)
			}
		})
	}
}
