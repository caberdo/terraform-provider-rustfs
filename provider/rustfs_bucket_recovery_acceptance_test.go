// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccBucketRecovery_refusesLiveVolume exercises the real recovery route
// against an existing bucket, which every disk still holds. It cannot produce a
// real orphan: that requires removing bucket volume directories out of band
// while the server is running, which the acceptance harness must not do. The
// recovery path itself is covered by the httptest lifecycle table; this test
// proves the route is reachable, the generation fence reaches the server, and a
// refusal never deletes the bucket.
func TestAccBucketRecovery_refusesLiveVolume(t *testing.T) {
	testAccPreCheck(t)

	client := testAccAdminClient()
	version, raw, err := client.ServerVersion(context.Background())
	if err != nil {
		t.Skipf("cannot determine the RustFS version, skipping: %v", err)
	}
	if !version.AtLeast(rustfs.Version{Major: 1, Minor: 0, Patch: 2}) {
		t.Skipf("orphaned bucket recovery ships in RustFS 1.0.2; this server reports %s", raw)
	}

	bucketName := fmt.Sprintf("tf-test-recovery-%d", acctest.RandInt())
	// A random non-nil UUID: the server must accept the request shape and
	// refuse it on the volume check, not on the identity.
	incarnationID := uuid.NewString()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketRecoveryLiveVolumeConfig(bucketName, incarnationID),
				// A live bucket volume stops recovery before the identity check.
				// A cluster with site replication enabled stops it even earlier;
				// both refusals are actionable and neither deletes anything.
				ExpectError: regexp.MustCompile(`still holds the bucket volume|site replication enabled`),
			},
			{
				// The failed recovery must not have touched the bucket: the
				// bucket from step 1 is still in state, so dropping the
				// recovery resource plans no change.
				Config:             testAccBucketRecoveryBucketOnlyConfig(bucketName),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Import is the only live path that exercises ImportState and
				// Read for this resource: a successful Create needs a real
				// orphan, which CI cannot produce. The record is persisted so
				// the next step can verify a second import against it.
				Config:             testAccBucketRecoveryLiveVolumeConfig(bucketName, incarnationID),
				ResourceName:       "rustfs_bucket_recovery.test",
				ImportState:        true,
				ImportStateId:      bucketName + "/" + incarnationID,
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					var imported *terraform.InstanceState
					for _, state := range states {
						if state.Ephemeral.Type == "rustfs_bucket_recovery" {
							if imported != nil {
								return fmt.Errorf("expected one imported recovery state, got more than one")
							}
							imported = state
						}
					}
					if imported == nil {
						return fmt.Errorf("no rustfs_bucket_recovery state was imported")
					}
					attrs := imported.Attributes
					for attr, want := range map[string]string{
						"bucket":                 bucketName,
						"incarnation_id":         incarnationID,
						"retired_incarnation_id": incarnationID,
						"id":                     incarnationID,
					} {
						if got := attrs[attr]; got != want {
							return fmt.Errorf("imported %s = %q, want %q", attr, got, want)
						}
					}
					return nil
				},
			},
			{
				Config:            testAccBucketRecoveryLiveVolumeConfig(bucketName, incarnationID),
				ResourceName:      "rustfs_bucket_recovery.test",
				ImportState:       true,
				ImportStateId:     bucketName + "/" + incarnationID,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccBucketRecoveryLiveVolumeConfig(bucket, incarnationID string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_recovery" "test" {
  bucket         = rustfs_bucket.test.name
  incarnation_id = "%s"
  depends_on     = [rustfs_bucket.test]
}
`, bucket, incarnationID)
}

func testAccBucketRecoveryBucketOnlyConfig(bucket string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}
`, bucket)
}
