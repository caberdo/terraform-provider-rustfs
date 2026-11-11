// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestAccConfigResource(t *testing.T) {
	scope := fmt.Sprintf("notify_webhook:tfaccept%d", acctest.RandInt())
	// A second scope drives the RequiresReplace path: changing sub_system has
	// to produce a replace plan and a real replacement, not an in-place update.
	replacementScope := fmt.Sprintf("notify_webhook:tfaccept%d", acctest.RandInt())
	missingScope := fmt.Sprintf("notify_webhook:tfaccept%dmissing", acctest.RandInt())
	resourceName := "rustfs_config.test"

	const (
		originalEndpoint = "http://127.0.0.1:18080/rustfs/events"
		updatedEndpoint  = "http://127.0.0.1:18080/rustfs/events_v2"
		driftedEndpoint  = "http://127.0.0.1:18080/rustfs/events_drift"
		replacedEndpoint = "http://127.0.0.1:18080/rustfs/events_replaced"
	)

	// Pre-cleanup in case a previous interrupted run left the targets behind.
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		Ssl:          false,
	})
	_ = client.DeleteConfig(context.Background(), scope)
	_ = client.DeleteConfig(context.Background(), replacementScope)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccPreCheck(t) },
		CheckDestroy:             testAccCheckConfigDestroy,
		Steps: []resource.TestStep{
			// Create: the server is the authority for the stored settings.
			{
				Config: testAccConfigConfig(scope, originalEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "sub_system", scope),
					resource.TestCheckResourceAttr(resourceName, "settings.%", "1"),
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", originalEndpoint),
					resource.TestCheckResourceAttr(resourceName, "id", scope),
					testAccCheckConfigServerSetting(scope, "endpoint", originalEndpoint),
				),
			},
			// In-place update: read back from the server, not just from state.
			{
				Config: testAccConfigConfig(scope, updatedEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", updatedEndpoint),
					testAccCheckConfigServerSetting(scope, "endpoint", updatedEndpoint),
				),
			},
			// Revert to the original endpoint.
			{
				Config: testAccConfigConfig(scope, originalEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", originalEndpoint),
					testAccCheckConfigServerSetting(scope, "endpoint", originalEndpoint),
				),
			},
			// Computed consistency: with the configuration unchanged the plan
			// must be empty, computed id included.
			{
				Config:             testAccConfigConfig(scope, originalEndpoint),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Out-of-band drift: the server setting is rewritten behind
			// Terraform's back after the apply, so the refresh plan has to
			// propose the configured value again. The check runs before the
			// post-apply plans, which is why a non-empty plan is expected; the
			// non-refresh plan cannot see the mutation, so the assertion on the
			// proposed values belongs to the post-refresh plan.
			{
				Config:             testAccConfigConfig(scope, originalEndpoint),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckConfigServerExists(scope),
					testAccMutateConfigOutOfBand(scope, "endpoint", driftedEndpoint),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(resourceName, tfjsonpath.New("settings").AtMapKey("endpoint"), knownvalue.StringExact(originalEndpoint)),
					},
				},
			},
			// Convergence: applying the unchanged configuration repairs the
			// drift and the server holds the configured value again.
			{
				Config: testAccConfigConfig(scope, originalEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", originalEndpoint),
					testAccCheckConfigServerSetting(scope, "endpoint", originalEndpoint),
				),
			},
			// Import: the import ID is the bare sub-system scope.
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        scope,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "sub_system",
			},
			// An ID that names no existing scope must fail loudly instead of
			// producing a half-filled state.
			{
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: missingScope,
				ExpectError:   regexp.MustCompile(`(?i)Cannot import non-existent remote object`),
			},
			// A RequiresReplace attribute change is planned as a replacement,
			// not as an in-place update.
			{
				Config:             testAccConfigConfig(replacementScope, originalEndpoint),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
						plancheck.ExpectKnownValue(resourceName, tfjsonpath.New("sub_system"), knownvalue.StringExact(replacementScope)),
					},
				},
			},
			// Apply the replacement so the replacement path really runs.
			{
				Config: testAccConfigConfig(replacementScope, replacedEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "sub_system", replacementScope),
					resource.TestCheckResourceAttr(resourceName, "id", replacementScope),
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", replacedEndpoint),
					testAccCheckConfigServerSetting(replacementScope, "endpoint", replacedEndpoint),
					// The replacement destroyed the previous scope, so the old
					// instance is gone from the server.
					testAccCheckConfigServerAbsent(scope),
				),
			},
			// The replaced instance is importable with the same identifier.
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        replacementScope,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "sub_system",
			},
			// The replaced instance is consistent with the configuration.
			{
				Config:             testAccConfigConfig(replacementScope, replacedEndpoint),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccConfigResourceRetainedSettings pins the refresh contract on a live
// server: a setting that sits in the scope but is not declared in the
// configuration is server-retained, not managed, so it must not produce a plan.
// RustFS 1.0.5 retains unsupported, legacy and alias keys at their original
// paths (rustfs/rustfs#8803); this fixture uses an accepted setting instead of
// an unknown one so it works on every version in the matrix and does not depend
// on the server rejecting or accepting an invented key.
func TestAccConfigResourceRetainedSettings(t *testing.T) {
	scope := fmt.Sprintf("notify_webhook:tfretained%d", acctest.RandInt())
	resourceName := "rustfs_config.test"
	const endpoint = "http://127.0.0.1:18080/rustfs/retained"

	// Pre-cleanup in case a previous interrupted run left the scope behind.
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		Ssl:          false,
	})
	_ = client.DeleteConfig(context.Background(), scope)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccPreCheck(t) },
		CheckDestroy:             testAccCheckConfigDestroy,
		Steps: []resource.TestStep{
			// Apply only the managed key.
			{
				Config: testAccConfigConfig(scope, endpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "settings.%", "1"),
					testAccCheckConfigServerSetting(scope, "endpoint", endpoint),
				),
			},
			// A setting written to the scope behind Terraform's back is kept by
			// the server but not declared in the configuration, so the refresh
			// ignores it and the plan stays empty. Adopting it would plan its
			// removal, the update would send only the configured keys, the
			// server would keep it, and the next read would adopt it again: a
			// perpetual diff. The check runs before the post-refresh plan.
			{
				Config:             testAccConfigConfig(scope, endpoint),
				ExpectNonEmptyPlan: false,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckConfigServerExists(scope),
					testAccMutateConfigOutOfBand(scope, "queue_limit", "12345"),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Ignoring the retained setting is not deleting it: the server
			// still holds it after the empty plan.
			{
				Config: testAccConfigConfig(scope, endpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckConfigServerSetting(scope, "endpoint", endpoint),
					testAccCheckConfigServerSetting(scope, "queue_limit", "12345"),
				),
			},
		},
	})
}

func testAccConfigConfig(scope, endpoint string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_config" "test" {
  sub_system = "%s"
  settings = {
    endpoint = "%s"
  }
}
`, scope, endpoint)
}

// testAccCheckConfigDestroy verifies the config scope was removed so the
// server configuration is left unchanged when the test exits.
func testAccCheckConfigDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_config" {
			continue
		}
		scope := rs.Primary.Attributes["sub_system"]
		client := rustfs.New(&rustfs.RustfsAdminConfig{
			AccessKey:    os.Getenv("RUSTFS_USER"),
			AccessSecret: os.Getenv("RUSTFS_SECRET"),
			Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
			Ssl:          false,
		})
		_, err := client.GetConfig(context.Background(), scope)
		if err == nil {
			return fmt.Errorf("config scope %s still exists after destroy", scope)
		}
		if !strings.Contains(err.Error(), "not found") {
			return err
		}
	}
	return nil
}

// testAccCheckConfigServerExists asserts the scope is readable from the server,
// independently of what Terraform has in state.
func testAccCheckConfigServerExists(scope string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig(context.Background(), scope)
		if err != nil {
			return fmt.Errorf("config scope %s is not readable from the server: %w", scope, err)
		}
		if len(kvs) == 0 {
			return fmt.Errorf("config scope %s has no settings on the server", scope)
		}
		return nil
	}
}

// testAccCheckConfigServerAbsent asserts the scope is gone from the server,
// which is what proves a replacement really destroyed the old instance instead
// of only planning to.
func testAccCheckConfigServerAbsent(scope string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig(context.Background(), scope)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return nil
			}
			return fmt.Errorf("failed to read config scope %s from the server: %w", scope, err)
		}
		return fmt.Errorf("config scope %s still exists on the server after replacement (got %v)", scope, kvs)
	}
}

// testAccCheckConfigServerSetting reads one setting back from the server, so a
// change is asserted against the stored configuration and not merely against
// the value Terraform echoed into state.
func testAccCheckConfigServerSetting(scope, key, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig(context.Background(), scope)
		if err != nil {
			return fmt.Errorf("failed to read config scope %s from the server: %w", scope, err)
		}
		for _, kv := range kvs {
			if kv.Key != key {
				continue
			}
			if kv.Value != want {
				return fmt.Errorf("config scope %s setting %q on the server is %q, want %q", scope, key, kv.Value, want)
			}
			return nil
		}
		return fmt.Errorf("config scope %s has no setting %q on the server (got %v)", scope, key, kvs)
	}
}

// testAccMutateConfigOutOfBand rewrites one setting of a config scope directly
// through the admin client, bypassing Terraform, to simulate drift or a
// server-retained key. The server rewrites the whole scope, so every key
// currently stored is sent back with the single requested setting replaced, or
// appended when the scope does not have it yet.
func testAccMutateConfigOutOfBand(scope, key, value string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig(context.Background(), scope)
		if err != nil {
			return fmt.Errorf("failed to read config scope %s before the out-of-band change: %w", scope, err)
		}
		replaced := false
		for i := range kvs {
			if kvs[i].Key == key {
				kvs[i].Value = value
				replaced = true
			}
		}
		if !replaced {
			kvs = append(kvs, rustfs.ConfigKV{Key: key, Value: value})
		}
		if err := client.SetConfig(context.Background(), scope, kvs); err != nil {
			return fmt.Errorf("failed to change config scope %s out of band: %w", scope, err)
		}
		return nil
	}
}

// rustfs105LegacyConfigPayload is the get-config-kv rendering RustFS 1.0.5
// produces for a MinIO-migrated identity_openid scope (rustfs/rustfs#8803):
// the keys the provider manages (config_url, client_id), an unsupported key the
// server now retains at its original path (legacy_scopes), and the legacy alias
// branch (openid) which the server keeps alongside identity_openid and keeps in
// sync. Before 1.0.5 those extra keys were dropped on save, so a read could not
// see them.
const rustfs105LegacyConfigPayload = `identity_openid config_url="https://idp.example.test/.well-known/openid-configuration" client_id="rustfs" legacy_scopes="openid"` + "\n" +
	`openid config_url="https://idp.example.test/.well-known/openid-configuration" client_id="rustfs" claim_name="groups"` + "\n"

// TestConfigResourceReadIgnoresServerRetainedKeys pins the refresh contract:
// after an apply the resource manages the keys the configuration declared, and
// a refresh keeps only those keys even when the server returns more. This is what stops a RustFS 1.0.5 deployment with a
// migrated or legacy config from producing a perpetual diff: without the
// filter, Read adopts legacy_scopes/claim_name into state, the next plan
// removes them, Update sends only the configured keys, the server keeps the
// extras, and the read after that adopts them again.
func TestConfigResourceReadIgnoresServerRetainedKeys(t *testing.T) {
	ctx := context.Background()

	client, rec := routedClient(t,
		requestRoute{method: http.MethodPut, pathContains: "set-config-kv", status: http.StatusOK},
		requestRoute{method: http.MethodGet, pathContains: "get-config-kv", status: http.StatusOK, body: rustfs105LegacyConfigPayload},
	)
	h := newCRUDHarness(t, NewConfigResource(), client)

	plan := &ConfigResourceModel{
		SubSystem: types.StringValue("identity_openid"),
		Settings: types.MapValueMust(types.StringType, map[string]attr.Value{
			"config_url": types.StringValue("https://idp.example.test/.well-known/openid-configuration"),
			"client_id":  types.StringValue("rustfs"),
		}),
	}
	created := h.create(plan)
	requireNoDiagnostics(t, created.Diagnostics)

	var applied ConfigResourceModel
	requireNoDiagnostics(t, created.State.Get(ctx, &applied))
	// Create stores exactly the configured set; this is the managed set the
	// refresh has to preserve.
	assertConfigSettings(t, applied.Settings, map[string]string{
		"config_url": "https://idp.example.test/.well-known/openid-configuration",
		"client_id":  "rustfs",
	})

	refreshed := h.read(&applied)
	requireNoDiagnostics(t, refreshed.Diagnostics)
	rec.assertRequest(t, "get-config-kv")

	var read ConfigResourceModel
	requireNoDiagnostics(t, refreshed.State.Get(ctx, &read))
	assertConfigSettings(t, read.Settings, map[string]string{
		"config_url": "https://idp.example.test/.well-known/openid-configuration",
		"client_id":  "rustfs",
	})
}

// TestConfigResourceReadStillReportsManagedKeyDrift guards the other half of
// the contract: filtering out server-retained keys must not hide a change to a
// key the configuration does manage, and must not keep a managed key the server
// no longer reports. Either case has to reach state so the plan proposes an
// update.
func TestConfigResourceReadStillReportsManagedKeyDrift(t *testing.T) {
	ctx := context.Background()

	client, _ := routedClient(t, requestRoute{
		method:       http.MethodGet,
		pathContains: "get-config-kv",
		status:       http.StatusOK,
		// client_id is missing from the scope, config_url was rewritten out of
		// band, and the retained keys are still there.
		body: `identity_openid config_url="https://other.example.test/.well-known/openid-configuration" legacy_scopes="openid"` + "\n",
	})
	h := newCRUDHarness(t, NewConfigResource(), client)

	state := &ConfigResourceModel{
		SubSystem: types.StringValue("identity_openid"),
		Settings: types.MapValueMust(types.StringType, map[string]attr.Value{
			"config_url": types.StringValue("https://idp.example.test/.well-known/openid-configuration"),
			"client_id":  types.StringValue("rustfs"),
		}),
	}
	resp := h.read(state)
	requireNoDiagnostics(t, resp.Diagnostics)

	var read ConfigResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &read))
	assertConfigSettings(t, read.Settings, map[string]string{
		"config_url": "https://other.example.test/.well-known/openid-configuration",
	})
}

// TestConfigResourceReadImportAdoptsWholeScope covers import, where there is no
// prior state to define a managed set: the read has to adopt every key the
// scope holds, including the ones the server retains, or an imported resource
// would start out empty and the first plan would discard them.
func TestConfigResourceReadImportAdoptsWholeScope(t *testing.T) {
	ctx := context.Background()

	client, _ := routedClient(t, requestRoute{
		method:       http.MethodGet,
		pathContains: "get-config-kv",
		status:       http.StatusOK,
		body:         rustfs105LegacyConfigPayload,
	})
	h := newCRUDHarness(t, NewConfigResource(), client)

	// What ImportState leaves behind: the sub_system, and a null settings map.
	resp := h.read(&ConfigResourceModel{
		SubSystem: types.StringValue("identity_openid"),
		Settings:  nullStringMap(),
	})
	requireNoDiagnostics(t, resp.Diagnostics)

	var read ConfigResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &read))
	assertConfigSettings(t, read.Settings, map[string]string{
		"config_url":    "https://idp.example.test/.well-known/openid-configuration",
		"client_id":     "rustfs",
		"legacy_scopes": "openid",
		"claim_name":    "groups",
	})
}

// TestConfigResourceReadEmptyManagedSetStaysEmpty pins that an explicitly empty
// settings map is a managed set with no keys, not an import: a configuration
// that declares no settings must not adopt the scope's retained keys either.
func TestConfigResourceReadEmptyManagedSetStaysEmpty(t *testing.T) {
	ctx := context.Background()

	client, _ := routedClient(t, requestRoute{
		method:       http.MethodGet,
		pathContains: "get-config-kv",
		status:       http.StatusOK,
		body:         rustfs105LegacyConfigPayload,
	})
	h := newCRUDHarness(t, NewConfigResource(), client)

	resp := h.read(&ConfigResourceModel{
		SubSystem: types.StringValue("identity_openid"),
		Settings:  types.MapValueMust(types.StringType, map[string]attr.Value{}),
	})
	requireNoDiagnostics(t, resp.Diagnostics)

	var read ConfigResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &read))
	if read.Settings.IsNull() {
		t.Fatal("settings = null, want an empty map")
	}
	assertConfigSettings(t, read.Settings, map[string]string{})
}

// assertConfigSettings compares a settings map against the expected key/value
// pairs, so a test fails on both a missing and an unexpected key.
func assertConfigSettings(t *testing.T, got types.Map, want map[string]string) {
	t.Helper()

	if got.IsNull() || got.IsUnknown() {
		t.Fatalf("settings = %v, want %v", got, want)
	}
	have := make(map[string]string, len(got.Elements()))
	for key, value := range got.Elements() {
		str, ok := value.(types.String)
		if !ok {
			t.Fatalf("setting %q has unexpected type %T", key, value)
		}
		have[key] = str.ValueString()
	}
	if !maps.Equal(have, want) {
		t.Fatalf("settings = %v, want %v", have, want)
	}
}

// Migrated from main's read/delete/create error-path work.
func TestConfigResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name         string
		routes       []requestRoute
		settings     types.Map
		wantRemoved  bool
		wantSummary  string
		wantDetail   string
		wantEndpoint string
		wantSettings map[string]string
	}{
		{
			// Probed against RustFS latest: get-config-kv answers 400
			// InvalidRequest "config subsystem 'x' not found" for a scope the
			// server does not know.
			name:        "unknown subsystem is removed from state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "config subsystem 'tf-test' not found")}},
			wantRemoved: true,
		},
		{
			name:        "unknown target is removed from state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "config target 'primary' not found for subsystem 'notify_webhook'")}},
			wantRemoved: true,
		},
		{
			// A successful body with no rendered keys is not evidence the scope
			// is gone (an unset subsystem answers with its defaults), so state
			// is kept instead of planning a destructive recreation.
			name:   "scope with an empty body keeps state",
			routes: []requestRoute{{status: http.StatusOK, body: ""}},
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading config",
			wantDetail:  "Could not read config, unexpected error: ",
		},
		{
			// A refresh keeps the managed keys and mirrors their server
			// values; a key the server retains but the configuration never
			// declared is not adopted (see
			// TestConfigResourceReadIgnoresServerRetainedKeys).
			name:         "configured scope mirrors the managed server settings",
			routes:       []requestRoute{{status: http.StatusOK, body: "notify_webhook endpoint=" + `"http://example.test"` + " enable=" + `"on"` + " legacy_queue_limit=" + `"100000"` + "\n"}},
			settings:     types.MapValueMust(types.StringType, map[string]attr.Value{"endpoint": types.StringValue("http://before.test")}),
			wantEndpoint: "http://example.test",
			wantSettings: map[string]string{"endpoint": "http://example.test"},
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewConfigResource(), client)

			settings := tc.settings
			if settings.IsNull() {
				settings = nullStringMap()
			}
			resp := h.read(&ConfigResourceModel{SubSystem: types.StringValue("tf-test"), Settings: settings})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertStatePresent(t, resp.State)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)
			assertStatePresent(t, resp.State)
			assertStateString(t, resp.State, path.Root("settings").AtMapKey("endpoint"), tc.wantEndpoint)
			if tc.wantSettings != nil {
				var got types.Map
				if diags := resp.State.GetAttribute(context.Background(), path.Root("settings"), &got); diags.HasError() {
					t.Fatalf("reading the settings state: %v", diags)
				}
				assertConfigSettings(t, got, tc.wantSettings)
			}
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: del-config-kv answers 400
			// InvalidRequest "unsupported config subsystem 'x'" once the scope
			// is gone, so an already deleted scope must not fail the destroy.
			name:   "already deleted scope is not an error",
			routes: []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "unsupported config subsystem 'tf-test'")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting config",
			wantDetail:  "Could not delete config, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewConfigResource(), client)

			resp := h.delete(&ConfigResourceModel{SubSystem: types.StringValue("tf-test"), Settings: nullStringMap()})

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected settings report a diagnostic and write no state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "invalid config value 'enable'")}},
			wantSummary: "Error setting config",
			wantDetail:  "Could not set config, unexpected error: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewConfigResource(), client)
			plan := &ConfigResourceModel{
				SubSystem: types.StringValue("tf-test"),
				Settings:  types.MapValueMust(types.StringType, map[string]attr.Value{"enable": types.StringValue("on")}),
			}

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
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "invalid config value 'enable'")}},
			wantSummary: "Error updating config",
			wantDetail:  "Could not update config, unexpected error: ",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewConfigResource(), client)
			plan := &ConfigResourceModel{
				SubSystem: types.StringValue("tf-test"),
				Settings:  types.MapValueMust(types.StringType, map[string]attr.Value{"enable": types.StringValue("on")}),
			}

			resp := h.update(plan, plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
