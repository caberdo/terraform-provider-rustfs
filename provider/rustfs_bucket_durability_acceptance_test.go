// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccBucketDurability_basic(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-durability-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_durability.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDurabilityDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketDurabilityConfig(bucketName, "strict"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketDurabilityExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "bucket", bucketName),
					resource.TestCheckResourceAttr(resourceName, "mode", "strict"),
				),
			},
			{
				// Computed consistency: the post-apply plan must be empty.
				Config:             testAccBucketDurabilityConfig(bucketName, "strict"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
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

func TestAccBucketDurability_update(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-durability-%d", acctest.RandInt())
	otherBucketName := bucketName + "-other"
	resourceName := "rustfs_bucket_durability.test"
	refSelf := "rustfs_bucket.test.name"
	refOther := "rustfs_bucket.other.name"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDurabilityDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketDurabilityRefConfig(bucketName, refSelf, "strict"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketDurabilityExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "mode", "strict"),
				),
			},
			{
				// In-place update: assert the mode read back from the server.
				Config: testAccBucketDurabilityRefConfig(bucketName, refSelf, "none"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketDurabilityExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "mode", "none"),
				),
			},
			{
				// Revert to the original mode.
				Config: testAccBucketDurabilityRefConfig(bucketName, refSelf, "strict"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketDurabilityExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "mode", "strict"),
				),
			},
			{
				// Computed consistency: the post-apply plan must be empty.
				Config:             testAccBucketDurabilityRefConfig(bucketName, refSelf, "strict"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// The only RequiresReplace() attribute is "bucket"; pointing the
				// resource at the second bucket must plan a replacement.
				Config:             testAccBucketDurabilityRefConfig(bucketName, refOther, "strict"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace)},
				},
			},
			{
				// Apply the replacement so the replacement path really runs.
				Config: testAccBucketDurabilityRefConfig(bucketName, refOther, "strict"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketDurabilityExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "bucket", otherBucketName),
					resource.TestCheckResourceAttr(resourceName, "mode", "strict"),
				),
			},
			{
				// Out-of-band drift: change the server behind Terraform's back.
				// The check runs before the post-apply plan, which must detect it.
				Config:             testAccBucketDurabilityRefConfig(bucketName, refOther, "strict"),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketDurabilityExists(resourceName),
					testAccMutateBucketDurabilityOutOfBand(t, otherBucketName, "none"),
				),
			},
			{
				// Converge back to the configured mode.
				Config: testAccBucketDurabilityRefConfig(bucketName, refOther, "strict"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketDurabilityExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "mode", "strict"),
				),
			},
		},
	})
}

func testAccBucketDurabilityConfig(bucket, mode string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_durability" "test" {
  bucket     = rustfs_bucket.test.name
  mode       = "%s"
  depends_on = [rustfs_bucket.test]
}
`, bucket, mode)
}

// testAccBucketDurabilityRefConfig creates two buckets so the durability
// resource can be re-pointed at the second one to exercise the RequiresReplace
// plan modifier on "bucket". bucketRef must be one of the "rustfs_bucket.*.name"
// references declared here.
func testAccBucketDurabilityRefConfig(bucket, bucketRef, mode string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket" "other" {
  name = "%s-other"
}

resource "rustfs_bucket_durability" "test" {
  bucket     = %s
  mode       = "%s"
  depends_on = [rustfs_bucket.test, rustfs_bucket.other]
}
`, bucket, bucket, bucketRef, mode)
}

func testAccCheckBucketDurabilityExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			return fmt.Errorf("no bucket set")
		}
		client := testAccDurabilityClient()
		d, err := client.GetBucketDurability(context.Background(), bucketName)
		if err != nil {
			return fmt.Errorf("error reading bucket durability: %s", err)
		}
		if d.Mode != rs.Primary.Attributes["mode"] {
			return fmt.Errorf("durability mode %q does not match state %q", d.Mode, rs.Primary.Attributes["mode"])
		}
		return nil
	}
}

// testAccMutateBucketDurabilityOutOfBand changes the bucket's durability mode
// directly through the admin API, bypassing Terraform, to create drift.
func testAccMutateBucketDurabilityOutOfBand(t *testing.T, bucket, mode string) resource.TestCheckFunc {
	t.Helper()

	return func(s *terraform.State) error {
		client := testAccDurabilityClient()
		if _, err := client.SetBucketDurability(context.Background(), bucket, rustfs.BucketDurability{Bucket: bucket, Mode: mode}); err != nil {
			return fmt.Errorf("out-of-band durability mutation failed: %s", err)
		}
		return nil
	}
}

func testAccCheckBucketDurabilityDestroy(s *terraform.State) error {
	client := testAccDurabilityClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_bucket_durability" {
			continue
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			continue
		}
		// The override is cleared on destroy; an empty mode means the bucket
		// inherits the process-wide durability mode again. A read error means
		// the bucket itself is already gone, which also counts as destroyed.
		d, err := client.GetBucketDurability(context.Background(), bucketName)
		if err != nil {
			continue
		}
		if d.Mode != "" {
			return fmt.Errorf("durability override for bucket %s still exists", bucketName)
		}
	}
	return nil
}

func testAccDurabilityClient() rustfs.RustfsAdmin {
	return rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}
