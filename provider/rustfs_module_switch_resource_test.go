// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
) // originalModuleSwitch captures the server's module switch state before the
// test mutates it, so the test can restore the original state afterwards.
var originalModuleSwitch *rustfs.ModuleSwitchState

func readModuleSwitchState(t *testing.T) *rustfs.ModuleSwitchState {
	t.Helper()
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
	})
	state, err := client.GetModuleSwitches(context.Background())
	if err != nil {
		t.Fatalf("failed to read original module switches: %v", err)
	}
	return state
}

// testAccEnsureModuleEnabled makes sure a feature module ("notify" or "audit")
// is enabled before the test body runs, and restores the switch state it
// changed when the test finishes. Acceptance tests that only need the module as
// a precondition (the notification and audit target tests) use it so the CI
// environment does not have to pin the module with an environment variable.
//
// The admin API only accepts switch updates for console/default sourced
// modules: when a module is pinned by an environment variable the server
// refuses the update. That state belongs to the deployment rather than to the
// test, so the test skips instead of failing.

// moduleSwitchMutex serializes the tests that use testAccEnsureModuleEnabled.
// Two parallel acceptance tests both needing the notify module would otherwise
// race: the first one to finish restores the switch to disabled while the
// other is still running. The lock is held until the test's cleanup runs, so a
// test's restore happens before the next caller observes the module state.
var moduleSwitchMutex sync.Mutex

func testAccEnsureModuleEnabled(t *testing.T, module string) {
	t.Helper()

	moduleSwitchMutex.Lock()
	t.Cleanup(moduleSwitchMutex.Unlock)

	client := testAccRustClient()
	original, err := client.GetModuleSwitches(context.Background())
	if err != nil {
		t.Fatalf("failed to read module switches: %v", err)
	}

	enabled, source := original.NotifyEnabled, original.NotifySource
	if module == "audit" {
		enabled, source = original.AuditEnabled, original.AuditSource
	}
	if enabled {
		return
	}
	if source == rustfs.ModuleSwitchSourceEnv {
		t.Skipf("the %s module is pinned by an environment variable; skipping: enable it via the deployment environment instead", module)
	}

	// Only flip the module under test; keep the effective value of the other
	// switch so a pinned sibling module is not part of the change.
	update := rustfs.ModuleSwitchUpdate{
		NotifyEnabled: original.NotifyEnabled,
		AuditEnabled:  original.AuditEnabled,
	}
	if module == "audit" {
		update.AuditEnabled = true
	} else {
		update.NotifyEnabled = true
	}
	if _, err := client.SetModuleSwitches(context.Background(), update); err != nil {
		if strings.Contains(err.Error(), "managed by environment variable") {
			t.Skipf("the %s module is pinned by an environment variable; skipping: %v", module, err)
		}
		t.Fatalf("failed to enable the %s module: %v", module, err)
	}
	t.Cleanup(func() {
		restore := rustfs.ModuleSwitchUpdate{
			NotifyEnabled: original.NotifyEnabled,
			AuditEnabled:  original.AuditEnabled,
		}
		if _, err := client.SetModuleSwitches(context.Background(), restore); err != nil {
			t.Errorf("failed to restore module switches: %v", err)
		}
	})
}

func TestAccModuleSwitchResource(t *testing.T) {
	// The module switches are global server state also toggled by
	// testAccEnsureModuleEnabled, so hold the same lock for the whole test to
	// keep the read-back assertions deterministic.
	moduleSwitchMutex.Lock()
	t.Cleanup(moduleSwitchMutex.Unlock)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck: func() {
			testAccPreCheck(t)
			originalModuleSwitch = readModuleSwitchState(t)
			t.Cleanup(func() {
				client := rustfs.New(&rustfs.RustfsAdminConfig{
					AccessKey:    os.Getenv("RUSTFS_USER"),
					AccessSecret: os.Getenv("RUSTFS_SECRET"),
					Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
				})
				update := rustfs.ModuleSwitchUpdate{
					NotifyEnabled: originalModuleSwitch.NotifyEnabled,
					AuditEnabled:  originalModuleSwitch.AuditEnabled,
				}
				if _, err := client.SetModuleSwitches(context.Background(), update); err != nil {
					t.Errorf("failed to restore original module switches: %v", err)
				}
			})
		},
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
resource "rustfs_module_switch" "test" {
  notify_enabled = true
  audit_enabled  = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "id", "module-switches"),
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "notify_enabled", "true"),
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "audit_enabled", "true"),
				),
			},
			{
				Config: testAccProviderConfig() + `
resource "rustfs_module_switch" "test" {
  notify_enabled = false
  audit_enabled  = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "notify_enabled", "false"),
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "audit_enabled", "true"),
				),
			},
			// Explicit computed-consistency check: the post-apply plan for the
			// configuration that was just applied must be empty.
			{
				Config: testAccProviderConfig() + `
resource "rustfs_module_switch" "test" {
  notify_enabled = false
  audit_enabled  = true
}
`,
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Revert the switch and assert the server read it back.
			{
				Config: testAccProviderConfig() + `
resource "rustfs_module_switch" "test" {
  notify_enabled = true
  audit_enabled  = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "notify_enabled", "true"),
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "audit_enabled", "true"),
				),
			},
			// Import reads the switches back from the server, so the state must
			// match the configuration applied above.
			{
				ResourceName:                         "rustfs_module_switch.test",
				ImportState:                          true,
				ImportStateId:                        "module-switches",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "id",
			},
		},
	})
}

func TestModuleSwitchResourceSchema(t *testing.T) {
	r := NewModuleSwitchResource()
	resp := &fwresource.SchemaResponse{}
	r.Schema(context.TODO(), fwresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{"id", "notify_enabled", "audit_enabled"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}
}

func TestModuleSwitchUpdateFromModel(t *testing.T) {
	update := moduleSwitchUpdateFromModel(ModuleSwitchResourceModel{
		NotifyEnabled: types.BoolValue(true),
		AuditEnabled:  types.BoolValue(false),
	})
	if !update.NotifyEnabled || update.AuditEnabled {
		t.Fatalf("unexpected update: %+v", update)
	}
}

// TestModuleSwitchResourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: every failing API call in Create, Read and
// Update, and the destroy that must not depend on the server at all.
//
// The switches are a cluster-wide singleton: GET module-switches always answers
// 200 with at least the defaults (probed against RustFS latest), and the admin
// API has no endpoint that removes them, so there is no "gone" read to cover.
func TestModuleSwitchResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
		wantNotify  bool
		wantAudit   bool
	}{
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading module switches",
			wantDetail:  "Could not read module switches: ",
		},
		{
			name:       "server state is mirrored into state",
			routes:     []requestRoute{{status: http.StatusOK, body: `{"notify_enabled":true,"audit_enabled":false,"notify_source":"console","audit_source":"console"}`}},
			wantNotify: true,
			wantAudit:  false,
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewModuleSwitchResource(), client)

			resp := h.read(&ModuleSwitchResourceModel{
				ID:            types.StringValue("module-switches"),
				NotifyEnabled: types.BoolValue(false),
				AuditEnabled:  types.BoolValue(true),
			})

			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertStatePresent(t, resp.State)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)
			assertStatePresent(t, resp.State)

			var notify types.Bool
			if diags := resp.State.GetAttribute(context.Background(), path.Root("notify_enabled"), &notify); diags.HasError() {
				t.Fatalf("state notify_enabled: %v", diags)
			}
			if notify.ValueBool() != tc.wantNotify {
				t.Errorf("state notify_enabled = %v, want %v", notify.ValueBool(), tc.wantNotify)
			}
			var audit types.Bool
			if diags := resp.State.GetAttribute(context.Background(), path.Root("audit_enabled"), &audit); diags.HasError() {
				t.Fatalf("state audit_enabled: %v", diags)
			}
			if audit.ValueBool() != tc.wantAudit {
				t.Errorf("state audit_enabled = %v, want %v", audit.ValueBool(), tc.wantAudit)
			}
		})
	}

	t.Run("delete: is a no-op so an already gone switch set is never an error", func(t *testing.T) {
		// The admin API exposes no reset endpoint, so Delete never talks to
		// the server and cannot fail when the switches are already gone.
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewModuleSwitchResource(), client)

		resp := h.delete(&ModuleSwitchResourceModel{
			ID:            types.StringValue("module-switches"),
			NotifyEnabled: types.BoolValue(true),
			AuditEnabled:  types.BoolValue(false),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "module-switches")
	})

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected switch update reports a diagnostic and writes no state",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "notify module is pinned by an environment variable")}},
			wantSummary: "Error setting module switches",
			wantDetail:  "Could not set module switches: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewModuleSwitchResource(), client)
			plan := &ModuleSwitchResourceModel{NotifyEnabled: types.BoolValue(true), AuditEnabled: types.BoolValue(true)}

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	updateCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected update reports a diagnostic",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error updating module switches",
			wantDetail:  "Could not update module switches: ",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewModuleSwitchResource(), client)
			plan := &ModuleSwitchResourceModel{NotifyEnabled: types.BoolValue(true), AuditEnabled: types.BoolValue(false)}

			resp := h.update(plan, plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewModuleSwitchResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
