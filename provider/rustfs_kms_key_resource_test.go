// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const kmsKeyTestResourceName = "rustfs_kms_key.test"

func TestAccKmsKeyResource(t *testing.T) {
	name := fmt.Sprintf("tf-kms-key-%d", acctest.RandInt())
	// `name` carries RequiresReplace, so a genuine replacement needs a second
	// name; the replacement is planned and then really applied.
	replacementName := fmt.Sprintf("tf-kms-key-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccKmsKeyPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckKmsKeyDestroy,
		Steps: []resource.TestStep{
			// Create the key enabled.
			{
				Config: testAccKmsKeyConfig(name, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "name", name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
					resource.TestCheckResourceAttrSet(kmsKeyTestResourceName, "key_id"),
					resource.TestCheckResourceAttrSet(kmsKeyTestResourceName, "created_at"),
					testAccCheckKmsKeyServerEnabled(true),
				),
			},
			// Metadata is applied in place and read back from the server.
			{
				Config: testAccKmsKeyMetadataConfig(name, "managed by terraform", "test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "description", "managed by terraform"),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "tags.environment", "test"),
					testAccCheckKmsKeyMetadata("managed by terraform", "test"),
				),
			},
			{
				Config: testAccKmsKeyMetadataConfig(name, "updated by terraform", "staging"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "description", "updated by terraform"),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "tags.environment", "staging"),
					testAccCheckKmsKeyMetadata("updated by terraform", "staging"),
				),
			},
			// In-place update: disable the key and assert the server really
			// reports the new value, not just the state.
			{
				Config: testAccKmsKeyConfig(name, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "false"),
					testAccCheckKmsKeyServerEnabled(false),
				),
			},
			// Revert the in-place change.
			{
				Config: testAccKmsKeyConfig(name, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
					testAccCheckKmsKeyServerEnabled(true),
				),
			},
			// Computed consistency: an explicit second plan over the state the
			// previous step just produced must be empty.
			{
				Config:             testAccKmsKeyConfig(name, true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Replacement contract for the RequiresReplace attribute `name`.
			{
				Config:             testAccKmsKeyConfig(replacementName, true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(kmsKeyTestResourceName, plancheck.ResourceActionReplace)},
				},
			},
			// Apply the replacement so the real delete/create path runs.
			{
				Config: testAccKmsKeyConfig(replacementName, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(replacementName),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "name", replacementName),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
					testAccCheckKmsKeyServerEnabled(true),
				),
			},
			// Import must reproduce the state the provider just applied.
			// `skip_destroy` is a client-side default that import cannot
			// reconstruct (the server never stores it), so it is ignored.
			{
				ResourceName:                         kmsKeyTestResourceName,
				ImportState:                          true,
				ImportStateId:                        replacementName,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"skip_destroy", "deletion_window_days", "rotate_trigger"},
			},
			// Out-of-band drift: the server is changed behind Terraform's back,
			// so the post-apply (post-refresh) plan must propose the in-place
			// update that brings the server back to the configuration.
			{
				Config:             testAccKmsKeyConfig(replacementName, true),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(replacementName),
					testAccMutateKmsKeyOutOfBand(t),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(kmsKeyTestResourceName, plancheck.ResourceActionUpdate)},
				},
			},
			// …and applying that plan converges back to the configured value.
			{
				Config: testAccKmsKeyConfig(replacementName, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(replacementName),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
					testAccCheckKmsKeyServerEnabled(true),
				),
			},
		},
	})
}

// testAccKmsKeyPreCheck verifies the environment and that the running RustFS
// backend supports KMS key creation. The Local dev backend does; the Static
// (read-only) backend does not. When the running backend cannot create keys
// the whole test skips gracefully instead of failing.
func testAccKmsKeyPreCheck(t *testing.T) {
	testAccPreCheck(t)

	client := newKmsKeyAccClient()
	//#nosec G117 — the probe key name is a public identifier, not a secret
	probeName := fmt.Sprintf("tf-kms-precheck-%d", acctest.RandInt())
	probeKey, err := client.CreateKmsKey(context.Background(), probeName)
	if err != nil {
		t.Skipf("KMS key creation is not supported on the running backend, skipping: %v", err)
	}
	// Clean up the probe key (scheduled deletion; cancellable server-side).
	if delErr := client.DeleteKmsKey(context.Background(), probeKey.KeyID); delErr != nil {
		t.Logf("warning: failed to clean up probe key %s: %v", probeKey.KeyID, delErr)
	}
}

func testAccKmsKeyConfig(name string, enabled bool) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_kms_key" "test" {
  name         = %q
  enabled      = %t
  skip_destroy = false
}
`, name, enabled)
}

func testAccKmsKeyMetadataConfig(name, description, environment string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_kms_key" "test" {
  name         = %q
  enabled      = true
  skip_destroy = false
  description  = %q
  tags = {
    environment = %q
  }
}
`, name, description, environment)
}

// testAccCheckKmsKeyMetadata proves the description and tag live on the
// server, not only in Terraform state.
func testAccCheckKmsKeyMetadata(description, environment string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[kmsKeyTestResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", kmsKeyTestResourceName)
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			return fmt.Errorf("no key_id set in state")
		}
		key, err := newKmsKeyAccClient().DescribeKmsKey(context.Background(), keyID)
		if err != nil {
			return fmt.Errorf("error describing KMS key %s: %s", keyID, err)
		}
		if key.Description == nil || *key.Description != description {
			return fmt.Errorf("KMS key %s description = %v, want %q", keyID, key.Description, description)
		}
		if key.Tags["environment"] != environment {
			return fmt.Errorf("KMS key %s environment tag = %q, want %q", keyID, key.Tags["environment"], environment)
		}
		return nil
	}
}

// testAccCheckKmsKeyExists asserts the key recorded in state is really the one
// the server holds under the expected name.
func testAccCheckKmsKeyExists(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[kmsKeyTestResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", kmsKeyTestResourceName)
		}
		if rs.Primary.Attributes["name"] != name {
			return fmt.Errorf("wrong name in state: %s", rs.Primary.Attributes["name"])
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			return fmt.Errorf("no key_id set in state")
		}

		key, err := newKmsKeyAccClient().DescribeKmsKey(context.Background(), keyID)
		if err != nil {
			return fmt.Errorf("error describing KMS key %s: %s", keyID, err)
		}
		if key.KeyID != keyID {
			return fmt.Errorf("described key id mismatch: got %s, want %s", key.KeyID, keyID)
		}
		return nil
	}
}

// testAccCheckKmsKeyServerEnabled reads the key back from the server and
// asserts both the state and the server-side key state carry `enabled`. This
// is the read-back proof for the in-place enable/disable update: the provider
// must not merely echo the planned value into state.
func testAccCheckKmsKeyServerEnabled(enabled bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[kmsKeyTestResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", kmsKeyTestResourceName)
		}
		if got := rs.Primary.Attributes["enabled"]; got != fmt.Sprintf("%t", enabled) {
			return fmt.Errorf("enabled in state = %q, want %t", got, enabled)
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			return fmt.Errorf("no key_id set in state")
		}
		key, err := newKmsKeyAccClient().DescribeKmsKey(context.Background(), keyID)
		if err != nil {
			return fmt.Errorf("error describing KMS key %s: %s", keyID, err)
		}
		if got := key.KeyState == "Enabled"; got != enabled {
			return fmt.Errorf("KMS key %s reports key_state %q, want enabled=%t", keyID, key.KeyState, enabled)
		}
		return nil
	}
}

// testAccMutateKmsKeyOutOfBand flips the server-side key state behind
// Terraform's back, leaving the state file claiming the opposite value. The
// next plan must therefore be non-empty.
func testAccMutateKmsKeyOutOfBand(t *testing.T) resource.TestCheckFunc {
	t.Helper()

	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[kmsKeyTestResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", kmsKeyTestResourceName)
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			return fmt.Errorf("no key_id set in state")
		}
		wantEnabled := rs.Primary.Attributes["enabled"] == "true"

		client := newKmsKeyAccClient()
		var err error
		if wantEnabled {
			err = client.DisableKmsKey(context.Background(), keyID)
		} else {
			err = client.EnableKmsKey(context.Background(), keyID)
		}
		if err != nil {
			return fmt.Errorf("out-of-band state change of KMS key %s failed: %s", keyID, err)
		}

		key, err := client.DescribeKmsKey(context.Background(), keyID)
		if err != nil {
			return fmt.Errorf("error describing KMS key %s after out-of-band change: %s", keyID, err)
		}
		if got := key.KeyState == "Enabled"; got == wantEnabled {
			return fmt.Errorf("out-of-band change did not take effect: key %s still reports key_state %q", keyID, key.KeyState)
		}
		t.Logf("out-of-band drift applied: KMS key %s is now %s", keyID, key.KeyState)
		return nil
	}
}

func testAccCheckKmsKeyDestroy(s *terraform.State) error {
	client := newKmsKeyAccClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_kms_key" {
			continue
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			continue
		}
		key, err := client.DescribeKmsKey(context.Background(), keyID)
		if err != nil {
			// Key gone (or describe refused): that satisfies destruction.
			continue
		}
		if key.KeyState != "PendingDeletion" {
			return fmt.Errorf("KMS key %s still present in state %s", keyID, key.KeyState)
		}
	}
	return nil
}

func newKmsKeyAccClient() *rustfs.RustfsAdmin {
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
	return &client
}

// kmsKeyLifecycleModel is a plan/state value with every attribute set; the
// computed key_id and created_at carry the values a previous apply would have
// stored.
func kmsKeyLifecycleModel() *kmsKeyResourceModel {
	return &kmsKeyResourceModel{
		Name:               types.StringValue("tf-key"),
		KeyID:              types.StringValue("key-1"),
		CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
		Enabled:            types.BoolValue(true),
		SkipDestroy:        types.BoolValue(false),
		Description:        types.StringNull(),
		Tags:               nullStringMap(),
		RotateTrigger:      types.StringNull(),
		DeletionWindowDays: types.Int64Value(30),
	}
}

// kmsKeyResponse renders the key_metadata object the KMS admin API returns for
// create/describe (pkg/rustfs/kms.go).
func kmsKeyResponse(keyID, state, created string) string {
	return fmt.Sprintf(`{"success":true,"key_metadata":{"key_id":%q,"key_state":%q,"key_usage":"ENCRYPT_DECRYPT","creation_date":%q,"origin":"RustFS_KMS","key_manager":"CUSTOMER"}}`, keyID, state, created)
}

// TestKmsKeyResourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: a key that disappeared behind Terraform's
// back, a delete that arrives after the key is already gone, and every failing
// API call in Create, Read and Delete.
//
// The KMS service was not running on the probe host (GET kms/status answered
// 500 InternalError "KMS service not initialized"), so these cases drive the
// paths from canned responses in the shape pkg/rustfs/kms.go documents.
func TestKmsKeyResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		model       *kmsKeyResourceModel
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
		wantKeyID   string
		wantCreated string
	}{
		{
			// pkg/rustfs DescribeKmsKey turns a 200 body without key_metadata
			// into the response message, so "not found" is the missing-key text.
			name:        "key gone from the server is removed from state",
			routes:      []requestRoute{{status: http.StatusOK, body: `{"success":false,"message":"key not found","key_metadata":null}`}},
			wantRemoved: true,
		},
		{
			name:        "key present is refreshed from the server",
			routes:      []requestRoute{{status: http.StatusOK, body: kmsKeyResponse("key-1", "Disabled", "2026-02-02T00:00:00Z")}},
			wantCreated: "2026-02-02T00:00:00Z",
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading KMS key",
			wantDetail:  "Could not read KMS key, unexpected error: ",
		},
		{
			// Import path: no key_id in state, so the listing resolves one.
			name:        "import with no key id resolves the listing and is removed when unmatched",
			model:       &kmsKeyResourceModel{Name: types.StringValue("tf-key"), KeyID: types.StringNull(), CreatedAt: types.StringNull(), Enabled: types.BoolValue(true), SkipDestroy: types.BoolValue(false), Description: types.StringNull(), Tags: nullStringMap(), RotateTrigger: types.StringNull(), DeletionWindowDays: types.Int64Value(30)},
			routes:      []requestRoute{{status: http.StatusOK, body: `{"success":true,"keys":[]}`}},
			wantRemoved: true,
		},
		{
			name:        "import with no key id reports a failed listing",
			model:       &kmsKeyResourceModel{Name: types.StringValue("tf-key"), KeyID: types.StringNull(), CreatedAt: types.StringNull(), Enabled: types.BoolValue(true), SkipDestroy: types.BoolValue(false), Description: types.StringNull(), Tags: nullStringMap(), RotateTrigger: types.StringNull(), DeletionWindowDays: types.Int64Value(30)},
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error listing KMS keys",
			wantDetail:  "Could not resolve key id from name, unexpected error: ",
		},
		{
			name:  "import with no key id resolves the tagged key",
			model: &kmsKeyResourceModel{Name: types.StringValue("tf-key"), KeyID: types.StringNull(), CreatedAt: types.StringNull(), Enabled: types.BoolValue(true), SkipDestroy: types.BoolValue(false), Description: types.StringNull(), Tags: nullStringMap(), RotateTrigger: types.StringNull(), DeletionWindowDays: types.Int64Value(30)},
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "kms/keys/key-9f3", status: http.StatusOK, body: kmsKeyResponse("key-9f3", "Enabled", "2026-03-03T00:00:00Z")},
				{method: http.MethodGet, pathContains: "kms/keys", status: http.StatusOK, body: `{"success":true,"keys":[{"key_id":"key-9f3","tags":{"name":"tf-key"}}]}`},
			},
			wantKeyID: "key-9f3",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewKmsKeyResource(), client)

			model := tc.model
			if model == nil {
				model = kmsKeyLifecycleModel()
			}
			resp := h.read(model)

			switch {
			case tc.wantRemoved:
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
			case tc.wantSummary != "":
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertStatePresent(t, resp.State)
			default:
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
			}
			if tc.wantCreated != "" {
				assertStateString(t, resp.State, path.Root("created_at"), tc.wantCreated)
			}
			if tc.wantKeyID != "" {
				assertStateString(t, resp.State, path.Root("key_id"), tc.wantKeyID)
			}
		})
	}

	deleteCases := []struct {
		name        string
		model       *kmsKeyResourceModel
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "already deleted key is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: `{"success":false,"message":"key not found","key_metadata":null}`}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "kms service is not running")}},
			wantSummary: "Error deleting KMS key",
			wantDetail:  "Could not delete KMS key, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewKmsKeyResource(), client)

			model := tc.model
			if model == nil {
				model = kmsKeyLifecycleModel()
			}
			resp := h.delete(model)

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("delete: skip_destroy leaves the key alone", func(t *testing.T) {
		// No route at all: any request the resource made would be unrouted and
		// fail the test.
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewKmsKeyResource(), client)
		model := kmsKeyLifecycleModel()
		model.SkipDestroy = types.BoolValue(true)

		resp := h.delete(model)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/keys/delete")
	})

	t.Run("delete: successful destroy is silent", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{status: http.StatusOK, body: `{"success":true}`})
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		resp := h.delete(kmsKeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/delete")
	})

	createCases := []struct {
		name        string
		model       *kmsKeyResourceModel
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected key reports the server error and writes no state",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "kms service is not running")}},
			wantSummary: "Error creating KMS key",
			wantDetail:  "Could not create KMS key, unexpected error: ",
		},
		{
			name: "failed disable of a new key is reported",
			model: &kmsKeyResourceModel{
				Name:               types.StringValue("tf-key"),
				KeyID:              types.StringNull(),
				CreatedAt:          types.StringNull(),
				Enabled:            types.BoolValue(false),
				SkipDestroy:        types.BoolValue(false),
				Description:        types.StringNull(),
				Tags:               nullStringMap(),
				RotateTrigger:      types.StringNull(),
				DeletionWindowDays: types.Int64Value(30),
			},
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "kms/keys/disable", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "disable exploded")},
				{method: http.MethodPost, pathContains: "kms/keys", status: http.StatusOK, body: kmsKeyResponse("key-1", "Enabled", "2026-01-01T00:00:00Z")},
			},
			wantSummary: "Error disabling KMS key",
			wantDetail:  "Could not disable KMS key, unexpected error: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewKmsKeyResource(), client)

			model := tc.model
			if model == nil {
				model = kmsKeyLifecycleModel()
			}
			resp := h.create(model)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, model)
		})
	}

	t.Run("create: omitted computed fields adopt the server values", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusOK, body: kmsKeyResponse("key-1", "Enabled", "2026-01-01T00:00:00Z")})
		h := newCRUDHarness(t, NewKmsKeyResource(), client)
		plan := kmsKeyLifecycleModel()
		plan.KeyID = types.StringNull()
		plan.CreatedAt = types.StringNull()
		// Terraform sends an omitted Optional+Computed attribute as unknown.
		plan.Description = types.StringUnknown()
		plan.Tags = types.MapUnknown(types.StringType)

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		var got kmsKeyResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading state: %v", diags)
		}
		if got.Description.IsUnknown() {
			t.Error("description is still unknown after apply")
		}
		if got.Tags.IsUnknown() {
			t.Error("tags are still unknown after apply")
		}
	})

	t.Run("create: accepted key writes the server identity", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusOK, body: kmsKeyResponse("key-1", "Enabled", "2026-01-01T00:00:00Z")})
		h := newCRUDHarness(t, NewKmsKeyResource(), client)
		plan := kmsKeyLifecycleModel()
		plan.KeyID = types.StringNull()
		plan.CreatedAt = types.StringNull()

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("key_id"), "key-1")
		assertStateString(t, resp.State, path.Root("created_at"), "2026-01-01T00:00:00Z")
	})

	t.Run("update: rejected enable reports a diagnostic and keeps the prior state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodPost, pathContains: "kms/keys/disable", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "disable exploded")})
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		plan := kmsKeyLifecycleModel()
		plan.Enabled = types.BoolValue(false)
		state := kmsKeyLifecycleModel()

		resp := h.update(plan, state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating KMS key", "Could not update KMS key, unexpected error: ")
		assertStatePresent(t, resp.State)
	})

	t.Run("update: unchanged enable switch makes no request", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewKmsKeyResource(), client)
		plan := kmsKeyLifecycleModel()
		state := kmsKeyLifecycleModel()

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/keys")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewKmsKeyResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func TestKmsKeyResourceSchema(t *testing.T) {
	r := NewKmsKeyResource()
	sch := resourceSchemaOf(t, r)

	for _, want := range []string{"name", "key_id", "created_at", "enabled", "skip_destroy", "description", "tags", "rotate_trigger", "deletion_window_days"} {
		if _, ok := sch.Attributes[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}

	description, ok := sch.Attributes["description"].(schema.StringAttribute)
	if !ok || !description.Optional || !description.Computed {
		t.Errorf("description must be Optional and Computed: %#v", sch.Attributes["description"])
	}
	tags, ok := sch.Attributes["tags"].(schema.MapAttribute)
	if !ok || !tags.Optional || !tags.Computed || tags.ElementType != types.StringType {
		t.Errorf("tags must be an Optional+Computed string map: %#v", sch.Attributes["tags"])
	}
	rotate, ok := sch.Attributes["rotate_trigger"].(schema.StringAttribute)
	if !ok || !rotate.Optional {
		t.Errorf("rotate_trigger must be Optional: %#v", sch.Attributes["rotate_trigger"])
	}
	window, ok := sch.Attributes["deletion_window_days"].(schema.Int64Attribute)
	if !ok || !window.Optional || !window.Computed {
		t.Errorf("deletion_window_days must be Optional and Computed: %#v", sch.Attributes["deletion_window_days"])
	}
}

func TestKmsKeyResourceLifecycleUpdates(t *testing.T) {
	t.Parallel()

	t.Run("rotate trigger change calls rotate", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/rotate", status: http.StatusOK,
			body: kmsKeyResponse("key-1", "Enabled", "2026-01-01T00:00:00Z"),
		})
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		state := kmsKeyLifecycleModel()
		plan := kmsKeyLifecycleModel()
		plan.RotateTrigger = types.StringValue("rotate-2")

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/rotate")
		assertStateString(t, resp.State, path.Root("rotate_trigger"), "rotate-2")
	})

	t.Run("rotate on an unsupported backend names the capability", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/rotate", status: http.StatusNotImplemented,
			body: `{"error":"unsupported capability"}`,
		})
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		state := kmsKeyLifecycleModel()
		plan := kmsKeyLifecycleModel()
		plan.RotateTrigger = types.StringValue("rotate-2")

		resp := h.update(plan, state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error rotating KMS key", "does not support key rotation")
	})

	t.Run("removing the rotate trigger does not rotate", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		state := kmsKeyLifecycleModel()
		state.RotateTrigger = types.StringValue("rotate-2")
		plan := kmsKeyLifecycleModel()
		plan.RotateTrigger = types.StringNull()

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/keys/rotate")
	})

	t.Run("description change updates it in place", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/update-description", status: http.StatusOK,
			body: `{"success":true,"key_id":"key-1","key_metadata":{"key_id":"key-1","key_state":"Enabled","description":"new","creation_date":"2026-01-01T00:00:00Z"}}`,
		})
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		state := kmsKeyLifecycleModel()
		state.Description = types.StringValue("old")
		plan := kmsKeyLifecycleModel()
		plan.Description = types.StringValue("new")

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/update-description")
		assertStateString(t, resp.State, path.Root("description"), "new")
	})

	t.Run("adding a tag calls tag and never writes the reserved name", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/tag", status: http.StatusOK,
			body: `{"success":true,"key_id":"key-1","key_metadata":{"key_id":"key-1","key_state":"Enabled","tags":{"name":"tf-key","environment":"test"}}}`,
		})
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		state := kmsKeyLifecycleModel()
		plan := kmsKeyLifecycleModel()
		plan.Tags = types.MapValueMust(types.StringType, map[string]attr.Value{"environment": types.StringValue("test")})

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/tag")
		rec.assertNoRequest(t, "kms/keys/untag")
	})

	t.Run("removing a tag calls untag", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/untag", status: http.StatusOK,
			body: `{"success":true,"key_id":"key-1","key_metadata":{"key_id":"key-1","key_state":"Enabled","tags":{"name":"tf-key"}}}`,
		})
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		state := kmsKeyLifecycleModel()
		state.Tags = types.MapValueMust(types.StringType, map[string]attr.Value{"environment": types.StringValue("test")})
		plan := kmsKeyLifecycleModel()
		plan.Tags = types.MapValueMust(types.StringType, map[string]attr.Value{})

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/untag")
		rec.assertNoRequest(t, "kms/keys/tag")
	})

	t.Run("the reserved name tag is rejected at validation", func(t *testing.T) {
		client, _ := routedClient(t)
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		model := kmsKeyLifecycleModel()
		model.Tags = types.MapValueMust(types.StringType, map[string]attr.Value{"name": types.StringValue("other")})

		resp := h.validateConfig(model)

		assertDiagnosticDetail(t, resp.Diagnostics, "reserved")
	})
}

func TestKmsKeyResourceReadFiltersReservedTag(t *testing.T) {
	client, _ := routedClient(t, requestRoute{
		method: http.MethodGet, pathContains: "kms/keys/key-1", status: http.StatusOK,
		body: `{"success":true,"key_metadata":{"key_id":"key-1","key_state":"Enabled","description":"app key","creation_date":"2026-01-01T00:00:00Z","tags":{"name":"tf-key","environment":"test"}}}`,
	})
	h := newCRUDHarness(t, NewKmsKeyResource(), client)

	resp := h.read(kmsKeyLifecycleModel())

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("description"), "app key")

	var tags types.Map
	if diags := resp.State.GetAttribute(context.Background(), path.Root("tags"), &tags); diags.HasError() {
		t.Fatalf("read tags: %v", diags)
	}
	var got map[string]string
	if diags := tags.ElementsAs(context.Background(), &got, false); diags.HasError() {
		t.Fatalf("decode tags: %v", diags)
	}
	if _, ok := got["name"]; ok {
		t.Errorf("reserved name tag leaked into state: %+v", got)
	}
	if got["environment"] != "test" {
		t.Errorf("expected the environment tag, got %+v", got)
	}
}

func TestKmsKeyResourceDeleteUsesDeletionWindow(t *testing.T) {
	var got struct {
		KeyID               string `json:"key_id"`
		PendingWindowInDays *int64 `json:"pending_window_in_days"`
	}
	client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	h := newCRUDHarness(t, NewKmsKeyResource(), client)

	model := kmsKeyLifecycleModel()
	model.DeletionWindowDays = types.Int64Value(7)

	resp := h.delete(model)

	requireNoDiagnostics(t, resp.Diagnostics)
	if got.KeyID != "key-1" {
		t.Errorf("wrong key id: %s", got.KeyID)
	}
	if got.PendingWindowInDays == nil || *got.PendingWindowInDays != 7 {
		t.Errorf("wrong deletion window: %v", got.PendingWindowInDays)
	}
}

func TestKmsKeyResourceCreateMetadata(t *testing.T) {
	t.Parallel()

	t.Run("description and tags are applied after create", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "kms/keys/update-description", status: http.StatusOK, body: `{"success":true,"key_id":"key-1","key_metadata":{"key_id":"key-1","key_state":"Enabled","description":"app key"}}`},
			requestRoute{method: http.MethodPost, pathContains: "kms/keys/tag", status: http.StatusOK, body: `{"success":true,"key_id":"key-1","key_metadata":{"key_id":"key-1","key_state":"Enabled","tags":{"name":"tf-key","environment":"test"}}}`},
			requestRoute{method: http.MethodPost, pathContains: "kms/keys", status: http.StatusOK, body: kmsKeyResponse("key-1", "Enabled", "2026-01-01T00:00:00Z")},
		)
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		plan := kmsKeyLifecycleModel()
		plan.KeyID = types.StringNull()
		plan.CreatedAt = types.StringNull()
		plan.Description = types.StringValue("app key")
		plan.Tags = types.MapValueMust(types.StringType, map[string]attr.Value{"environment": types.StringValue("test")})

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/update-description")
		rec.assertRequest(t, "kms/keys/tag")
		assertStateString(t, resp.State, path.Root("description"), "app key")
	})

	t.Run("a metadata failure writes no partial state", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "kms/keys/update-description", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "update exploded")},
			requestRoute{method: http.MethodPost, pathContains: "kms/keys", status: http.StatusOK, body: kmsKeyResponse("key-1", "Enabled", "2026-01-01T00:00:00Z")},
		)
		h := newCRUDHarness(t, NewKmsKeyResource(), client)

		plan := kmsKeyLifecycleModel()
		plan.KeyID = types.StringNull()
		plan.CreatedAt = types.StringNull()
		plan.Description = types.StringValue("app key")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating KMS key description", "Could not update KMS key description, unexpected error: ")
		h.assertNoPartialState(resp, plan)
	})
}
