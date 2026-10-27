// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccQuotaResource_basic(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-quota-%d", acctest.RandInt())
	resourceName := "rustfs_quota.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckQuotaAndBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccQuotaConfig(bucketName, 100000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportStateId:                        bucketName,
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func TestAccQuotaResource_update(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-quota-%d", acctest.RandInt())
	resourceName := "rustfs_quota.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckQuotaAndBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccQuotaConfig(bucketName, 100000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
					testAccCheckQuotaOnServer(t, bucketName, 100000),
				),
			},
			{
				// rustfs_quota declares no RequiresReplace() attribute: both
				// "bucket" and "quota" are updated in place, so a changed quota
				// must plan an update and never a replacement.
				Config:             testAccQuotaConfig(bucketName, 200000),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				// Apply the change so the in-place update path really runs and
				// assert the value the server read back, not just the state.
				Config: testAccQuotaConfig(bucketName, 200000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "200000"),
					testAccCheckQuotaOnServer(t, bucketName, 200000),
				),
			},
			{
				// Revert to the original quota.
				Config: testAccQuotaConfig(bucketName, 100000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
					testAccCheckQuotaOnServer(t, bucketName, 100000),
				),
			},
			{
				// Computed consistency: the post-apply plan must be empty.
				Config:             testAccQuotaConfig(bucketName, 100000),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: the quota is deleted behind Terraform's
				// back, so the post-apply plan must propose putting it back with
				// an in-place update.
				Config:             testAccQuotaConfig(bucketName, 100000),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					testAccMutateQuotaOutOfBand(t, bucketName, 100000),
				),
			},
			{
				// The following apply converges back onto the configured quota.
				Config: testAccQuotaConfig(bucketName, 100000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
					testAccCheckQuotaOnServer(t, bucketName, 100000),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportStateId:                        bucketName,
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func testAccQuotaConfig(bucket string, quota int) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_quota" "test" {
  bucket     = rustfs_bucket.test.name
  quota      = %d
  depends_on = [rustfs_bucket.test]
}
`, bucket, quota)
}

func testAccCheckQuotaExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			return fmt.Errorf("no bucket set")
		}
		client := testAccQuotaClient()

		// Retry with the provider's own tolerance: a freshly started server
		// answers ServiceUnavailable until its scanner has computed the
		// bucket's authoritative usage, and the suite now creates enough
		// buckets concurrently that the first read can lose that race.
		if _, err := quotaReadWithRetry(context.Background(), bucketName, client.ReadQuota); err != nil {
			return fmt.Errorf("quota not found: %s", err)
		}
		return nil
	}
}

// testAccCheckQuotaOnServer reads the bucket quota straight from the admin API
// so an assertion never depends on Terraform's own state. The read tolerates
// the cold-start response a freshly created quota can produce, exactly like the
// provider's own read path does.
func testAccCheckQuotaOnServer(t *testing.T, bucket string, want int) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client := testAccQuotaClient()
		quota, err := quotaReadWithRetry(context.Background(), bucket, client.ReadQuota)
		if err != nil {
			return fmt.Errorf("could not read quota of bucket %s: %s", bucket, err)
		}
		if quota.Quota != want {
			return fmt.Errorf("expected server quota of bucket %s to be %d, got %d", bucket, want, quota.Quota)
		}
		return nil
	}
}

// testAccMutateQuotaOutOfBand deletes the bucket quota behind Terraform's back
// and waits until the server no longer reports the Terraform-managed value, so
// the refresh that follows deterministically observes drift.
func testAccMutateQuotaOutOfBand(t *testing.T, bucket string, managed int) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client := testAccQuotaClient()
		if err := client.DeletQuota(context.Background(), bucket); err != nil {
			return fmt.Errorf("could not delete quota of bucket %s out of band: %s", bucket, err)
		}

		for range quotaReadMaxAttempts {
			quota, err := client.ReadQuota(context.Background(), bucket)
			if err != nil {
				if isTransientQuotaError(err) {
					// Usage is not authoritative yet; look again.
					time.Sleep(time.Second)
					continue
				}
				// The server now reports the quota as absent.
				return nil
			}
			if quota.Quota != managed {
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("bucket %s still reports quota %d after the out-of-band delete", bucket, managed)
	}
}

func testAccCheckQuotaAndBucketDestroy(s *terraform.State) error {
	client := testAccQuotaClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_quota" {
			continue
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			continue
		}
		_, err := client.ReadQuota(context.Background(), bucketName)
		if err == nil {
			return fmt.Errorf("quota for bucket %s still exists", bucketName)
		}
	}
	return nil
}

func testAccQuotaClient() rustfs.RustfsAdmin {
	return rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}

// TestQuotaResourceLifecyclePaths covers the paths the acceptance suite cannot
// reach with a live server: a quota whose bucket disappeared behind Terraform's
// back, a delete of a bucket that is already gone, and every failing API call
// in Create, Read and Delete. The retry behaviour itself is owned by
// TestQuotaReadWithRetry and TestQuotaSetWithRetry in rustfs_quota_retry_test.go
// and is deliberately not duplicated here.
func TestQuotaResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: ReadQuota answers 404 NoSuchBucket
			// for a bucket that does not exist.
			name:        "missing bucket removes the quota from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading bucket quota",
			wantDetail:  "Could not read bucket quota, unexpected error: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewquotaResource(), client)

			resp := h.read(&quotaResourceModel{Bucket: types.StringValue("tf-test"), Quota: types.Int64Value(1000)})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: DeletQuota on a missing bucket
			// answers 500 InternalError "failed to capture bucket incarnation:
			// Bucket not found" because its first step captures the bucket
			// incarnation. A destroy of an already deleted bucket is the
			// desired end state and must not fail.
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "failed to capture bucket incarnation: Bucket not found")}},
		},
		{
			// A different InternalError is a real failure, not a missing
			// bucket, so the endpoint's own wording must not be bypassed.
			name:        "unrelated internal error is reported",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "disk exploded")}},
			wantSummary: "Error deleting bucket quota",
			wantDetail:  "Could not delete bucket quota, unexpected error: ",
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting bucket quota",
			wantDetail:  "Could not delete bucket quota, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewquotaResource(), client)

			resp := h.delete(&quotaResourceModel{Bucket: types.StringValue("tf-test"), Quota: types.Int64Value(1000)})

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
			name:        "rejected quota reports a diagnostic and writes no state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "quota must be a positive integer")}},
			wantSummary: "Error creating bucket quota",
			wantDetail:  "Could not create bucket quota, unexpected error: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewquotaResource(), client)
			plan := &quotaResourceModel{Bucket: types.StringValue("tf-test"), Quota: types.Int64Value(-1)}

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
			wantSummary: "Error updating bucket quota",
			wantDetail:  "Could not update bucket quota, unexpected error: ",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewquotaResource(), client)
			plan := &quotaResourceModel{Bucket: types.StringValue("tf-test"), Quota: types.Int64Value(2000)}

			resp := h.update(plan, plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewquotaResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
