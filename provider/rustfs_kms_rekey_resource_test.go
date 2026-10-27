// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestKmsRekeyResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewKmsRekeyResource())

	for _, want := range []string{"id", "buckets", "prefix", "job_id", "state", "current_bucket", "scanned", "rewrapped", "already_current", "not_applicable", "failed"} {
		if _, ok := sch.Attributes[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
	if _, ok := sch.Attributes["buckets"].(schema.ListAttribute); !ok {
		t.Errorf("buckets must be a list")
	}
	if _, ok := sch.Attributes["prefix"].(schema.StringAttribute); !ok {
		t.Errorf("prefix must be a string")
	}
	for _, computed := range []string{"id", "job_id", "state", "current_bucket", "scanned", "rewrapped", "already_current", "not_applicable", "failed"} {
		switch attr := sch.Attributes[computed].(type) {
		case schema.StringAttribute:
			if !attr.Computed {
				t.Errorf("%s must be computed", computed)
			}
		case schema.Int64Attribute:
			if !attr.Computed {
				t.Errorf("%s must be computed", computed)
			}
		default:
			t.Errorf("%s has unexpected type %T", computed, attr)
		}
	}
}

func kmsRekeyLifecycleModel() *kmsRekeyResourceModel {
	return &kmsRekeyResourceModel{
		ID:             types.StringValue("job-1"),
		Buckets:        types.ListNull(types.StringType),
		Prefix:         types.StringNull(),
		JobID:          types.StringValue("job-1"),
		State:          types.StringValue("running"),
		CurrentBucket:  types.StringValue("b1"),
		Scanned:        types.Int64Value(10),
		Rewrapped:      types.Int64Value(7),
		AlreadyCurrent: types.Int64Value(1),
		NotApplicable:  types.Int64Value(1),
		Failed:         types.Int64Value(1),
	}
}

func kmsRekeySnapshot(state string) string {
	return `{"job_id":"job-1","state":"` + state + `","buckets":["b1"],"current_bucket":"b1","scanned":10,"rewrapped":7,"already_current":1,"not_applicable":1,"failed":1}`
}

func TestKmsRekeyResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("create starts a sweep and records the snapshot", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/rekey", status: http.StatusOK,
			body: kmsRekeySnapshot("running"),
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		plan := kmsRekeyLifecycleModel()
		plan.ID = types.StringNull()
		plan.JobID = types.StringNull()
		plan.State = types.StringNull()
		plan.CurrentBucket = types.StringNull()
		plan.Scanned = types.Int64Null()
		plan.Rewrapped = types.Int64Null()
		plan.AlreadyCurrent = types.Int64Null()
		plan.NotApplicable = types.Int64Null()
		plan.Failed = types.Int64Null()
		plan.Buckets = types.ListValueMust(types.StringType, nil)

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/rekey")
		assertStateString(t, resp.State, path.Root("job_id"), "job-1")
		assertStateString(t, resp.State, path.Root("state"), "running")
	})

	t.Run("create reports an unsupported backend", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
			requestRoute{method: http.MethodGet, pathContains: "kms/config", status: http.StatusOK,
				body: `{"backend":"local","cache_enabled":true,"cache_max_keys":1000,"cache_ttl_seconds":300,"default_key_id":"rustfs-default-key"}`},
			requestRoute{method: http.MethodPost, pathContains: "kms/keys/rekey", status: http.StatusNotImplemented,
				body: `{"error":"unsupported"}`},
		)
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.create(kmsRekeyLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting KMS rekey",
			"POST /rustfs/admin/v3/kms/keys/rekey is served, but the local KMS backend does not support rewrapping data-key envelopes")
		for _, want := range []string{"Vault KV2", "RustFS server version: 1.0.3"} {
			assertDiagnosticDetail(t, resp.Diagnostics, want)
		}
	})

	t.Run("create reports an already running sweep", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/rekey", status: http.StatusConflict,
			body: `{"error":"a rekey sweep is already running","job_id":"job-9"}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.create(kmsRekeyLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting KMS rekey", "already running")
	})

	t.Run("read removes the resource when no sweep exists", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusNotFound,
			body: `{"error":"no rekey sweep has run"}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.read(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("read removes the resource when a different sweep is current", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusOK,
			body: `{"job_id":"job-2","state":"completed","buckets":["b1"],"current_bucket":null,"scanned":1,"rewrapped":1,"already_current":0,"not_applicable":0,"failed":0}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.read(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("read refreshes progress", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusOK,
			body: `{"job_id":"job-1","state":"completed","buckets":["b1"],"current_bucket":null,"scanned":99,"rewrapped":90,"already_current":9,"not_applicable":0,"failed":0}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.read(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("state"), "completed")

		var scanned types.Int64
		if diags := resp.State.GetAttribute(context.Background(), path.Root("scanned"), &scanned); diags.HasError() {
			t.Fatalf("read scanned: %v", diags)
		}
		if scanned.ValueInt64() != 99 {
			t.Errorf("scanned = %d, want 99", scanned.ValueInt64())
		}
	})

	t.Run("delete cancels a running sweep", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusOK, body: kmsRekeySnapshot("running")},
			requestRoute{method: http.MethodPost, pathContains: "kms/keys/rekey/cancel", status: http.StatusOK, body: kmsRekeySnapshot("cancelled")},
		)
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.delete(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/rekey/cancel")
	})

	t.Run("delete leaves a completed sweep alone", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusOK,
			body: kmsRekeySnapshot("completed"),
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.delete(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/keys/rekey/cancel")
	})

	t.Run("delete is idempotent when no sweep exists", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusNotFound,
			body: `{"error":"no rekey sweep has run"}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.delete(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/keys/rekey/cancel")
	})
}

func TestKmsRekeyResourceImportState(t *testing.T) {
	r := NewKmsRekeyResource()
	resp := &frameworkresource.ImportStateResponse{State: resourceState(t, resourceSchemaOf(t, r), kmsRekeyLifecycleModel())}

	importer, ok := r.(frameworkresource.ResourceWithImportState)
	if !ok {
		t.Fatal("resource does not implement resource.ResourceWithImportState")
	}
	importer.ImportState(context.Background(), frameworkresource.ImportStateRequest{ID: "job-9"}, resp)

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("id"), "job-9")
	assertStateString(t, resp.State, path.Root("job_id"), "job-9")
}

// testAccKmsRekeySkipUnlessCapability skips unless the deployment's KMS backend
// reports the given rewrap capability. The capability is server state, not an
// environment variable the test can set, and the skip prints what the server
// reported so an operator knows which backend the cell ran with.
func testAccKmsRekeySkipUnlessCapability(t *testing.T, want bool) {
	t.Helper()

	client := testAccRustClient()
	status, err := client.GetKmsStatus(context.Background())
	if err != nil {
		t.Skipf("this deployment does not serve the KMS status route: %v", err)
	}
	if got := status.Capabilities["rewrap"]; got != want {
		t.Skipf("KMS backend %q reports rewrap=%t; this case needs rewrap=%t", status.BackendType, got, want)
	}
}

// TestAccKmsRekeyResource_unsupportedBackend asserts that a backend which
// cannot re-wrap envelopes produces the provider's capability diagnostic
// instead of the raw 501 answer. The stock acceptance deployment (RustFS_KMS_
// BACKEND=local) is exactly such a backend, so this case runs on every cell.
func TestAccKmsRekeyResource_unsupportedBackend(t *testing.T) {
	testAccPreCheck(t)
	testAccKmsRekeySkipUnlessCapability(t, false)

	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				// The scope is a prefix no acceptance object uses, so a server
				// that unexpectedly accepts the start cannot move real data.
				Config: testAccKmsRekeyUnsupportedConfig(),
				// Terraform wraps the detail, so the pattern joins the
				// fragments it must contain instead of matching the layout.
				ExpectError: regexp.MustCompile(
					`(?s)Error starting KMS rekey` +
						`.*POST /rustfs/admin/v3/kms/keys/rekey` +
						`.*does not support rewrapping data-key\s+envelopes` +
						`.*Vault KV2 and Vault Transit can` +
						`.*RustFS server version: \d+\.\d+\.\d+`,
				),
			},
		},
	})
}

// TestAccKmsRekeyResource_start drives a real sweep on a backend that supports
// re-wrapping. It is opt-in: set RUSTFS_ACC_KMS_REKEY_BUCKET to a disposable
// bucket on a Vault-backed deployment. Without it the case skips, because a
// rekey sweep writes envelope metadata for every matched object.
func TestAccKmsRekeyResource_start(t *testing.T) {
	testAccPreCheck(t)
	testAccKmsRekeySkipUnlessCapability(t, true)

	bucket := os.Getenv("RUSTFS_ACC_KMS_REKEY_BUCKET")
	if bucket == "" {
		t.Skip("set RUSTFS_ACC_KMS_REKEY_BUCKET to a disposable bucket on a backend that supports rewrap")
	}

	resourceName := "rustfs_kms_rekey.test"

	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckKmsRekeyDestroyed,
		Steps: []tfresource.TestStep{
			{
				Config: testAccKmsRekeyConfig(bucket),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttrSet(resourceName, "job_id"),
					tfresource.TestCheckResourceAttrSet(resourceName, "state"),
					tfresource.TestCheckResourceAttrPair(resourceName, "id", resourceName, "job_id"),
					tfresource.TestCheckResourceAttr(resourceName, "buckets.#", "1"),
					tfresource.TestCheckResourceAttr(resourceName, "buckets.0", bucket),
				),
			},
			{
				// Progress counters are server state, so an unchanged
				// configuration must not produce a plan after a refresh.
				Config:   testAccKmsRekeyConfig(bucket),
				PlanOnly: true,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func testAccKmsRekeyUnsupportedConfig() string {
	return testAccProviderConfig() + `
resource "rustfs_kms_rekey" "test" {
  prefix = "tf-acc-kms-rekey-never-taken/"
}
`
}

func testAccKmsRekeyConfig(bucket string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_kms_rekey" "test" {
  buckets = [%q]
}
`, bucket)
}

// testAccCheckKmsRekeyDestroyed accepts the two states destroy can leave: no
// sweep record at all (the server keeps it in memory only), or a record that is
// no longer running. Cancellation is cooperative, so the residual "running"
// state is polled briefly instead of failing immediately.
func testAccCheckKmsRekeyDestroyed(_ *terraform.State) error {
	client := testAccRustClient()
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, err := client.GetKmsRekeyStatus(context.Background())
		if errors.Is(err, rustfs.ErrKmsRekeyNotRun) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading rekey status after destroy: %w", err)
		}
		if status.State != "running" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rekey sweep %s is still running after destroy", status.JobID)
		}
		time.Sleep(time.Second)
	}
}
