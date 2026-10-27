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
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const scannerUsageResetTestResponse = `{"status":"reset","mode":"full-rebuild",` +
	`"usage_state":"bootstrap-pending","leader_epoch":9,"next_cycle":42,` +
	`"reset_paths":["buckets/.usage.v2.json","buckets/.usage.json"]}`

func TestScannerUsageStateResetResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewScannerUsageStateResetResource())

	attrs := sch.GetAttributes()
	if len(attrs) != 9 {
		t.Errorf("expected 8 attributes, got %d: %v", len(attrs), attrs)
	}

	id, ok := attrs["id"].(schema.StringAttribute)
	if !ok || !id.Computed || id.Required || id.Optional {
		t.Errorf("id must be Computed only: %+v", attrs["id"])
	}

	triggers, ok := attrs["triggers"].(schema.MapAttribute)
	if !ok || !triggers.Optional || triggers.Required || triggers.Computed {
		t.Errorf("triggers must be Optional only: %+v", attrs["triggers"])
	}

	confirm, ok := attrs["confirm_full_rebuild"].(schema.BoolAttribute)
	if !ok || !confirm.Required || confirm.Optional || confirm.Computed {
		t.Errorf("confirm_full_rebuild must be Required only: %+v", attrs["confirm_full_rebuild"])
	}

	for _, name := range []string{"status", "mode", "usage_state"} {
		attr, ok := attrs[name].(schema.StringAttribute)
		if !ok || !attr.Computed || attr.Required || attr.Optional {
			t.Errorf("%s must be Computed only: %+v", name, attrs[name])
		}
	}

	for _, name := range []string{"leader_epoch", "next_cycle"} {
		attr, ok := attrs[name].(schema.Int64Attribute)
		if !ok || !attr.Computed || attr.Required || attr.Optional {
			t.Errorf("%s must be Computed only: %+v", name, attrs[name])
		}
	}

	paths, ok := attrs["reset_paths"].(schema.ListAttribute)
	if !ok || !paths.Computed || paths.Required || paths.Optional {
		t.Errorf("reset_paths must be Computed only: %+v", attrs["reset_paths"])
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestScannerUsageStateResetResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewScannerUsageStateResetResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_scanner_usage_state_reset" {
		t.Errorf("expected rustfs_scanner_usage_state_reset, got %s", resp.TypeName)
	}
}

func TestScannerUsageStateResetResourceValidateConfig(t *testing.T) {
	t.Run("requires the destructive acknowledgement", func(t *testing.T) {
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.validateConfig(&ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(false),
			ResetPaths:         types.ListNull(types.StringType),
		})

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Invalid scanner usage-state rebuild confirmation")
		if !strings.Contains(diagnostic.Detail(), "discards the durable usage baseline") {
			t.Errorf("detail = %q", diagnostic.Detail())
		}
	})

	t.Run("accepts an explicit true", func(t *testing.T) {
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.validateConfig(&ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
	})
}

func TestScannerUsageStateResetResourceCreate(t *testing.T) {
	t.Run("runs the destructive reset", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, scannerUsageResetTestResponse)
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), newUnitAllClient(srv))

		resp := h.create(&ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		request := recorder.only(t)
		if request.Method != http.MethodPost || request.Path != "/rustfs/admin/v3/scanner/usage-state/reset" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if got := string(request.Body); got != `{"mode":"full-rebuild"}` {
			t.Errorf("body = %q, want {\"mode\":\"full-rebuild\"}", got)
		}

		var got ScannerUsageStateResetResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ID.ValueString() != "scanner-usage-state-reset" {
			t.Errorf("id = %q", got.ID.ValueString())
		}
		if got.Status.ValueString() != "reset" || got.Mode.ValueString() != "full-rebuild" {
			t.Errorf("result = %q/%q", got.Status.ValueString(), got.Mode.ValueString())
		}
		if got.UsageState.ValueString() != "bootstrap-pending" {
			t.Errorf("usage_state = %q", got.UsageState.ValueString())
		}
		if got.LeaderEpoch.ValueInt64() != 9 || got.NextCycle.ValueInt64() != 42 {
			t.Errorf("epoch/cycle = %d/%d", got.LeaderEpoch.ValueInt64(), got.NextCycle.ValueInt64())
		}
		var paths []string
		if diags := got.ResetPaths.ElementsAs(context.Background(), &paths, false); diags.HasError() {
			t.Fatalf("reading reset_paths: %v", diags)
		}
		if len(paths) != 2 || paths[0] != "buckets/.usage.v2.json" {
			t.Errorf("reset_paths = %v", paths)
		}
	})

	t.Run("refuses an unacknowledged reset without calling the server", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)
		plan := &ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(false),
			ResetPaths:         types.ListNull(types.StringType),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid scanner usage-state rebuild confirmation", "must be true")
		rec.assertNoRequest(t, "scanner")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports a capability gap with the server version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "scanner/usage-state/reset", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)
		plan := &ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Scanner usage-state reset is not available on this RustFS server", "RustFS 1.0.3")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports a server failure and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method:       http.MethodPost,
			pathContains: "scanner/usage-state/reset",
			status:       http.StatusConflict,
			body:         s3ErrorXML("OperationAborted", "usage publication failed"),
		})
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)
		plan := &ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error resetting scanner usage state", "usage publication failed")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("retries a busy scanner leader and records the eventual result", func(t *testing.T) {
		restoreRetry := setScannerResetRetry(2, 0)
		t.Cleanup(restoreRetry)

		var requests int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.Header().Set("Content-Type", "application/xml")
			if requests <= 2 {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, s3ErrorXML("OperationAborted", "scanner leader lock is busy"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, scannerUsageResetTestResponse)
		}))
		t.Cleanup(srv.Close)

		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), newUnitAllClient(srv))
		resp := h.create(&ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		if requests != 3 {
			t.Errorf("requests = %d, want 3", requests)
		}
		assertStateString(t, resp.State, path.Root("usage_state"), "bootstrap-pending")
	})

	t.Run("fails after the busy retries are exhausted", func(t *testing.T) {
		restoreRetry := setScannerResetRetry(2, 0)
		t.Cleanup(restoreRetry)

		var requests int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, s3ErrorXML("OperationAborted", "scanner leader lock is busy"))
		}))
		t.Cleanup(srv.Close)

		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), newUnitAllClient(srv))
		plan := &ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error resetting scanner usage state", "scanner leader lock is busy")
		if requests != 3 {
			t.Errorf("requests = %d, want 3", requests)
		}
		h.assertNoPartialState(resp, plan)
	})
}

// setScannerResetRetry overrides the busy-lock retry budget for a test and
// returns the function that restores the production values.
func setScannerResetRetry(retries int, delay time.Duration) func() {
	previousRetries, previousDelay := scannerResetBusyRetries, scannerResetBusyDelay
	scannerResetBusyRetries, scannerResetBusyDelay = retries, delay
	return func() {
		scannerResetBusyRetries, scannerResetBusyDelay = previousRetries, previousDelay
	}
}

func TestScannerUsageStateResetResourceRead(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)

	resp := h.read(&ScannerUsageStateResetResourceModel{
		ID:                 types.StringValue("scanner-usage-state-reset"),
		Triggers:           nullStringMap(),
		ConfirmFullRebuild: types.BoolValue(true),
		Status:             types.StringValue("reset"),
		Mode:               types.StringValue("full-rebuild"),
		UsageState:         types.StringValue("bootstrap-pending"),
		LeaderEpoch:        types.Int64Value(9),
		NextCycle:          types.Int64Value(42),
		ResetPaths:         types.ListNull(types.StringType),
	})

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)
	assertStateString(t, resp.State, path.Root("usage_state"), "bootstrap-pending")
	rec.assertNoRequest(t, "scanner")
}

func TestScannerUsageStateResetResourceDelete(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)

	resp := h.delete(&ScannerUsageStateResetResourceModel{
		ID:                 types.StringValue("scanner-usage-state-reset"),
		Triggers:           nullStringMap(),
		ConfirmFullRebuild: types.BoolValue(true),
		ResetPaths:         types.ListNull(types.StringType),
	})

	requireNoDiagnostics(t, resp.Diagnostics)
	rec.assertNoRequest(t, "scanner")
}

func TestScannerUsageStateResetResourceUpdate(t *testing.T) {
	client, rec := routedClient(t, requestRoute{
		method:       http.MethodPost,
		pathContains: "scanner/usage-state/reset",
		status:       http.StatusOK,
		body:         scannerUsageResetTestResponse,
	})
	h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)

	state := &ScannerUsageStateResetResourceModel{
		ID:                 types.StringValue("scanner-usage-state-reset"),
		Triggers:           nullStringMap(),
		ConfirmFullRebuild: types.BoolValue(true),
		Status:             types.StringValue("reset"),
		Mode:               types.StringValue("full-rebuild"),
		UsageState:         types.StringValue("bootstrap-pending"),
		LeaderEpoch:        types.Int64Value(9),
		NextCycle:          types.Int64Value(42),
		ResetPaths:         types.ListNull(types.StringType),
	}
	resp := h.update(&ScannerUsageStateResetResourceModel{
		Triggers:           triggersMap(t, "run", "2"),
		ConfirmFullRebuild: types.BoolValue(true),
		ResetPaths:         types.ListNull(types.StringType),
	}, state)

	requireNoDiagnostics(t, resp.Diagnostics)
	rec.assertRequest(t, "scanner/usage-state/reset")
	assertStateString(t, resp.State, path.Root("usage_state"), "bootstrap-pending")
}

func TestScannerUsageStateResetResourceConfigure(t *testing.T) {
	t.Run("rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func TestScannerUsageStateResetResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("read: the reset has no server-side existence check and keeps state", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)

		resp := h.read(&ScannerUsageStateResetResourceModel{
			ID:                 types.StringValue("scanner-usage-state-reset"),
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		rec.assertNoRequest(t, "scanner")
	})

	t.Run("delete: the reset is not revertible and reports nothing", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)

		resp := h.delete(&ScannerUsageStateResetResourceModel{
			ID:                 types.StringValue("scanner-usage-state-reset"),
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "scanner")
	})

	t.Run("create: accepted reset records the server result", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodPost,
			pathContains: "scanner/usage-state/reset",
			status:       http.StatusOK,
			body:         scannerUsageResetTestResponse,
		})
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)

		resp := h.create(&ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "scanner-usage-state-reset")
		rec.assertRequest(t, "scanner/usage-state/reset")
	})

	t.Run("create: capability gap writes no state", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "scanner/usage-state/reset", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), client)
		plan := &ScannerUsageStateResetResourceModel{
			Triggers:           nullStringMap(),
			ConfirmFullRebuild: types.BoolValue(true),
			ResetPaths:         types.ListNull(types.StringType),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Scanner usage-state reset is not available on this RustFS server", "POST /rustfs/admin/v3/scanner/usage-state/reset")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewScannerUsageStateResetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func TestAccScannerUsageStateResetResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccScannerUsageStateResetPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
resource "rustfs_scanner_usage_state_reset" "test" {
  confirm_full_rebuild = true
  triggers = {
    run = "acceptance"
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rustfs_scanner_usage_state_reset.test", "id", "scanner-usage-state-reset"),
					resource.TestCheckResourceAttr("rustfs_scanner_usage_state_reset.test", "status", "reset"),
					resource.TestCheckResourceAttr("rustfs_scanner_usage_state_reset.test", "mode", "full-rebuild"),
					resource.TestCheckResourceAttrSet("rustfs_scanner_usage_state_reset.test", "usage_state"),
				),
			},
		},
	})
}

// testAccScannerUsageStateResetPreCheck proves the running server implements
// the reset route before the test config applies it. The probe is destructive,
// but the acceptance test performs the same reset immediately afterwards and
// the test cluster is disposable.
func testAccScannerUsageStateResetPreCheck(t *testing.T) {
	testAccScannerStatusPreCheck(t)

	client := testAccRustClient()
	_, err := resetWithScannerLeaderRetry(context.Background(), func() (rustfs.ScannerUsageStateResetResult, error) {
		return client.ResetScannerUsageState(context.Background())
	})
	if err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("scanner usage-state reset is not implemented by the running RustFS, skipping: %v", err)
		}
		if rustfs.IsScannerLeaderLockBusy(err) {
			t.Skipf("scanner usage-state reset precheck could not take the scanner leader lock, skipping: %v", err)
		}
		t.Fatalf("could not reset scanner usage state: %v", err)
	}
}
