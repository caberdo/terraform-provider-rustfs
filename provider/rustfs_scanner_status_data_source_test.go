// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const scannerStatusTestPayload = `{
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

func TestScannerStatusDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewScannerStatusDataSource())

	required := []string{
		"id", "status_json", "enabled", "disabled_reason", "freshness_state", "current_cycle",
		"current_cycle_active", "current_scan_mode", "last_cycle_end_unix_secs", "last_cycle_result",
		"last_cycle_duration_seconds", "last_cycle_objects_scanned", "last_cycle_directories_scanned",
		"cycles_completed", "failed_cycles", "superseded_cycles", "effective_cycle_interval_seconds",
		"usage_bootstrap_rebuild_pending", "usage_publication_state", "usage_last_durable_success_unix_secs",
		"usage_dirty_pending_buckets", "maintenance_primary_control", "cycle_recovery_state",
		"cycle_recovery_reason", "data_movement_paused", "pause_backlog_phase",
	}
	for _, name := range required {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
			t.Errorf("attribute %q must be Computed only", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}
}

func TestScannerStatusDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewScannerStatusDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_scanner_status" {
		t.Errorf("expected rustfs_scanner_status, got %s", resp.TypeName)
	}
}

func TestScannerStatusDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewScannerStatusDataSource().(*ScannerStatusDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewScannerStatusDataSource().(*ScannerStatusDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewScannerStatusDataSource().(*ScannerStatusDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestScannerStatusDataSourceRead(t *testing.T) {
	t.Run("surfaces the scanner state", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, scannerStatusTestPayload)
		d := NewScannerStatusDataSource().(*ScannerStatusDataSource)
		d.client = newUnitAllClient(srv)

		resp := readScannerStatus(t, d, dataSourceSchemaOf(t, d))
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/scanner/status" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got ScannerStatusDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ID.ValueString() != "scanner-status" {
			t.Errorf("id = %q", got.ID.ValueString())
		}
		if got.StatusJSON.ValueString() != scannerStatusTestPayload {
			t.Error("status_json does not carry the server body")
		}
		if !got.Enabled.ValueBool() {
			t.Error("enabled = false")
		}
		if got.FreshnessState.ValueString() != "fresh" {
			t.Errorf("freshness_state = %q", got.FreshnessState.ValueString())
		}
		if got.CurrentCycle.ValueInt64() != 42 || got.CurrentCycleActive.ValueBool() {
			t.Errorf("cycle = %d active=%v", got.CurrentCycle.ValueInt64(), got.CurrentCycleActive.ValueBool())
		}
		if got.CurrentScanMode.ValueString() != "normal" {
			t.Errorf("current_scan_mode = %q", got.CurrentScanMode.ValueString())
		}
		if got.LastCycleEndUnixSecs.ValueInt64() != 1760000000 {
			t.Errorf("last_cycle_end_unix_secs = %d", got.LastCycleEndUnixSecs.ValueInt64())
		}
		if got.LastCycleResult.ValueString() != "success" || got.LastCycleDurationSeconds.ValueFloat64() != 12.5 {
			t.Errorf("last cycle = %q/%v", got.LastCycleResult.ValueString(), got.LastCycleDurationSeconds.ValueFloat64())
		}
		if got.LastCycleObjectsScanned.ValueInt64() != 1234 || got.LastCycleDirectoriesScanned.ValueInt64() != 56 {
			t.Errorf("last cycle counters = %d/%d", got.LastCycleObjectsScanned.ValueInt64(), got.LastCycleDirectoriesScanned.ValueInt64())
		}
		if got.CyclesCompleted.ValueInt64() != 2 || got.FailedCycles.ValueInt64() != 1 || got.SupersededCycles.ValueInt64() != 2 {
			t.Errorf("cycle counters = %d/%d/%d", got.CyclesCompleted.ValueInt64(), got.FailedCycles.ValueInt64(), got.SupersededCycles.ValueInt64())
		}
		if got.EffectiveCycleIntervalSeconds.ValueInt64() != 60 || got.UsageBootstrapRebuildPending.ValueBool() {
			t.Errorf("schedule = %d pending=%v", got.EffectiveCycleIntervalSeconds.ValueInt64(), got.UsageBootstrapRebuildPending.ValueBool())
		}
		if got.UsagePublicationState.ValueString() != "published" || got.UsageDirtyPendingBuckets.ValueInt64() != 3 {
			t.Errorf("usage = %q dirty=%d", got.UsagePublicationState.ValueString(), got.UsageDirtyPendingBuckets.ValueInt64())
		}
		if got.UsageLastDurableSuccessUnixSecs.ValueInt64() != 1760000001 {
			t.Errorf("usage_last_durable_success_unix_secs = %d", got.UsageLastDurableSuccessUnixSecs.ValueInt64())
		}
		if got.MaintenancePrimaryControl.ValueString() != "none" {
			t.Errorf("maintenance_primary_control = %q", got.MaintenancePrimaryControl.ValueString())
		}
		if !got.DisabledReason.IsNull() {
			t.Errorf("disabled_reason = %q, want null", got.DisabledReason.ValueString())
		}
		if got.CycleRecoveryState.ValueString() != "healthy" || !got.CycleRecoveryReason.IsNull() {
			t.Errorf("cycle recovery = %q/%v", got.CycleRecoveryState.ValueString(), got.CycleRecoveryReason)
		}
		if got.DataMovementPaused.ValueBool() || got.PauseBacklogPhase.ValueString() != "idle" {
			t.Errorf("movement pause = %v/%q", got.DataMovementPaused.ValueBool(), got.PauseBacklogPhase.ValueString())
		}
	})

	t.Run("explains a capability gap with the server version", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rustfs/admin/v3/scanner/status":
				w.WriteHeader(http.StatusNotImplemented)
				_, _ = io.WriteString(w, `<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`)
			case "/rustfs/admin/v3/healthinfo":
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.3"}`)
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		t.Cleanup(srv.Close)

		d := NewScannerStatusDataSource().(*ScannerStatusDataSource)
		d.client = newUnitAllClient(srv)
		resp := readScannerStatus(t, d, dataSourceSchemaOf(t, d))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Scanner status is not available on this RustFS server")
		for _, want := range []string{"RustFS 1.0.3", "GET /rustfs/admin/v3/scanner/status", "Upgrade RustFS"} {
			if !strings.Contains(diagnostic.Detail(), want) {
				t.Errorf("detail %q does not contain %q", diagnostic.Detail(), want)
			}
		}
	})

	t.Run("reports an unexpected error without calling it a capability gap", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, `<Error><Code>InternalError</Code><Message>boom</Message></Error>`)
		d := NewScannerStatusDataSource().(*ScannerStatusDataSource)
		d.client = newUnitAllClient(srv)
		resp := readScannerStatus(t, d, dataSourceSchemaOf(t, d))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading scanner status")
		if !strings.Contains(diagnostic.Detail(), "boom") {
			t.Errorf("detail %q does not carry the server payload", diagnostic.Detail())
		}
		if strings.Contains(diagnostic.Detail(), "not available") {
			t.Errorf("a server error must not be reported as a capability gap: %q", diagnostic.Detail())
		}
	})
}

func readScannerStatus(t *testing.T, d *ScannerStatusDataSource, sch datasourceschema.Schema) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, ScannerStatusDataSourceModel{ID: types.StringNull()}),
	}, resp)
	return resp
}

func TestAccScannerStatusDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccScannerStatusPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_scanner_status" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_scanner_status.test", "id", "scanner-status"),
					resource.TestCheckResourceAttrSet("data.rustfs_scanner_status.test", "status_json"),
					resource.TestCheckResourceAttrSet("data.rustfs_scanner_status.test", "freshness_state"),
				),
			},
		},
	})
}

// testAccScannerStatusPreCheck skips only when the server answers the status
// route as an unsupported capability, so a failure with an actual server error
// still fails the test.
func testAccScannerStatusPreCheck(t *testing.T) {
	testAccPreCheck(t)
	client := testAccRustClient()
	if _, err := client.GetScannerStatus(context.Background()); err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("scanner status is not implemented by the running RustFS, skipping: %v", err)
		}
		t.Fatalf("could not read scanner status: %v", err)
	}
}
