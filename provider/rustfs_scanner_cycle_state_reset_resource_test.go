// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestScannerCycleStateResetResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewScannerCycleStateResetResource())

	attrs := sch.GetAttributes()
	if len(attrs) != 4 {
		t.Errorf("expected 4 attributes, got %d: %v", len(attrs), attrs)
	}

	id, ok := attrs["id"].(schema.StringAttribute)
	if !ok || !id.Computed || id.Required || id.Optional {
		t.Errorf("id must be Computed only: %+v", attrs["id"])
	}

	triggers, ok := attrs["triggers"].(schema.MapAttribute)
	if !ok || !triggers.Optional || triggers.Required || triggers.Computed {
		t.Errorf("triggers must be Optional only: %+v", attrs["triggers"])
	}

	for _, name := range []string{"status", "mode"} {
		attr, ok := attrs[name].(schema.StringAttribute)
		if !ok || !attr.Computed || attr.Required || attr.Optional {
			t.Errorf("%s must be Computed only: %+v", name, attrs[name])
		}
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestScannerCycleStateResetResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewScannerCycleStateResetResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_scanner_cycle_state_reset" {
		t.Errorf("expected rustfs_scanner_cycle_state_reset, got %s", resp.TypeName)
	}
}

func TestScannerCycleStateResetResourceCreate(t *testing.T) {
	t.Run("runs the reset", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodPost,
			pathContains: "scanner/cycle-state/reset",
			status:       http.StatusOK,
			body:         `{"status":"reset","mode":"full-rescan"}`,
		})
		h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)

		resp := h.create(&ScannerCycleStateResetResourceModel{Triggers: nullStringMap()})

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "scanner/cycle-state/reset")
		assertStateString(t, resp.State, path.Root("id"), "scanner-cycle-state-reset")
		assertStateString(t, resp.State, path.Root("status"), "reset")
		assertStateString(t, resp.State, path.Root("mode"), "full-rescan")
	})

	t.Run("reports a capability gap with the server version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "scanner/cycle-state/reset", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)
		plan := &ScannerCycleStateResetResourceModel{Triggers: nullStringMap()}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Scanner cycle-state reset is not available on this RustFS server", "RustFS 1.0.3")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports a server failure and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method:       http.MethodPost,
			pathContains: "scanner/cycle-state/reset",
			status:       http.StatusForbidden,
			body:         s3ErrorXML("AccessDenied", "nope"),
		})
		h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)
		plan := &ScannerCycleStateResetResourceModel{Triggers: nullStringMap()}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error resetting scanner cycle state", "AccessDenied")
		h.assertNoPartialState(resp, plan)
	})
}

func TestScannerCycleStateResetResourceRead(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)

	resp := h.read(&ScannerCycleStateResetResourceModel{
		ID:       types.StringValue("scanner-cycle-state-reset"),
		Status:   types.StringValue("reset"),
		Mode:     types.StringValue("full-rescan"),
		Triggers: nullStringMap(),
	})

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)
	assertStateString(t, resp.State, path.Root("status"), "reset")
	rec.assertNoRequest(t, "scanner")
}

func TestScannerCycleStateResetResourceDelete(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)

	resp := h.delete(&ScannerCycleStateResetResourceModel{ID: types.StringValue("scanner-cycle-state-reset"), Triggers: nullStringMap()})

	requireNoDiagnostics(t, resp.Diagnostics)
	rec.assertNoRequest(t, "scanner")
}

func TestScannerCycleStateResetResourceUpdate(t *testing.T) {
	client, rec := routedClient(t, requestRoute{
		method:       http.MethodPost,
		pathContains: "scanner/cycle-state/reset",
		status:       http.StatusOK,
		body:         `{"status":"reset","mode":"full-rescan"}`,
	})
	h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)

	state := &ScannerCycleStateResetResourceModel{
		ID:       types.StringValue("scanner-cycle-state-reset"),
		Status:   types.StringValue("reset"),
		Mode:     types.StringValue("full-rescan"),
		Triggers: nullStringMap(),
	}
	resp := h.update(&ScannerCycleStateResetResourceModel{Triggers: triggersMap(t, "run", "2")}, state)

	requireNoDiagnostics(t, resp.Diagnostics)
	rec.assertRequest(t, "scanner/cycle-state/reset")
	assertStateString(t, resp.State, path.Root("status"), "reset")
}

func triggersMap(t *testing.T, key, value string) types.Map {
	t.Helper()

	got, diags := types.MapValueFrom(context.Background(), types.StringType, map[string]string{key: value})
	if diags.HasError() {
		t.Fatalf("building triggers: %v", diags)
	}
	return got
}

func TestScannerCycleStateResetResourceConfigure(t *testing.T) {
	t.Run("rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewScannerCycleStateResetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func TestScannerCycleStateResetResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("read: the reset has no server-side existence check and keeps state", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)

		resp := h.read(&ScannerCycleStateResetResourceModel{ID: types.StringValue("scanner-cycle-state-reset"), Triggers: nullStringMap()})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		rec.assertNoRequest(t, "scanner")
	})

	t.Run("delete: the reset is not revertible and reports nothing", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)

		resp := h.delete(&ScannerCycleStateResetResourceModel{ID: types.StringValue("scanner-cycle-state-reset"), Triggers: nullStringMap()})

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "scanner")
	})

	t.Run("create: accepted reset records the server result", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodPost,
			pathContains: "scanner/cycle-state/reset",
			status:       http.StatusOK,
			body:         `{"status":"reset","mode":"full-rescan"}`,
		})
		h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)

		resp := h.create(&ScannerCycleStateResetResourceModel{Triggers: nullStringMap()})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "scanner-cycle-state-reset")
		rec.assertRequest(t, "scanner/cycle-state/reset")
	})

	t.Run("create: capability gap writes no state", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "scanner/cycle-state/reset", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		h := newCRUDHarness(t, NewScannerCycleStateResetResource(), client)
		plan := &ScannerCycleStateResetResourceModel{Triggers: nullStringMap()}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Scanner cycle-state reset is not available on this RustFS server", "POST /rustfs/admin/v3/scanner/cycle-state/reset")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewScannerCycleStateResetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func TestAccScannerCycleStateResetResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccScannerCycleStateResetPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
resource "rustfs_scanner_cycle_state_reset" "test" {
  triggers = {
    run = "acceptance"
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rustfs_scanner_cycle_state_reset.test", "id", "scanner-cycle-state-reset"),
					resource.TestCheckResourceAttr("rustfs_scanner_cycle_state_reset.test", "status", "reset"),
					resource.TestCheckResourceAttr("rustfs_scanner_cycle_state_reset.test", "mode", "full-rescan"),
				),
			},
		},
	})
}

// testAccScannerCycleStateResetPreCheck proves the running server implements
// the reset route before the test config applies it. The probe reset is
// harmless: the test resets the cycle state again.
func testAccScannerCycleStateResetPreCheck(t *testing.T) {
	testAccScannerStatusPreCheck(t)

	client := testAccRustClient()
	_, err := resetWithScannerLeaderRetry(context.Background(), func() (rustfs.ScannerCycleStateResetResult, error) {
		return client.ResetScannerCycleState(context.Background())
	})
	if err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("scanner cycle-state reset is not implemented by the running RustFS, skipping: %v", err)
		}
		if rustfs.IsScannerLeaderLockBusy(err) {
			t.Skipf("scanner cycle-state reset precheck could not take the scanner leader lock, skipping: %v", err)
		}
		t.Fatalf("could not reset scanner cycle state: %v", err)
	}
}
