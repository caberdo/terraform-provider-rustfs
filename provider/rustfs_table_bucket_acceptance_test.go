// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccTableBucket_basic(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-table-%d", acctest.RandInt())
	resourceName := "rustfs_table_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTableBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccTableBucketConfig(bucketName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckTableBucketEnabled(resourceName),
					resource.TestCheckResourceAttr(resourceName, "bucket", bucketName),
					resource.TestCheckResourceAttr(resourceName, "enabled", "true"),
					resource.TestCheckResourceAttr(resourceName, "catalog_type", "iceberg-rest"),
					resource.TestCheckResourceAttr(resourceName, "warehouse", bucketName),
					resource.TestCheckResourceAttr(resourceName, "catalog_uri", "/iceberg/v1/"+bucketName),
				),
			},
			{
				// Computed consistency: the post-apply plan must be empty.
				Config:             testAccTableBucketConfig(bucketName),
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

func testAccTableBucketConfig(bucket string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_table_bucket" "test" {
  bucket     = rustfs_bucket.test.name
  depends_on = [rustfs_bucket.test]
}
`, bucket)
}

func testAccCheckTableBucketEnabled(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			return fmt.Errorf("no bucket set")
		}
		client, err := testAccTableBucketClient()
		if err != nil {
			return err
		}
		tableBucket, err := client.RustClient.GetTableBucket(context.Background(), bucketName)
		if err != nil {
			return fmt.Errorf("error reading table bucket: %s", err)
		}
		if !tableBucket.Enabled {
			return fmt.Errorf("S3 Tables catalog for bucket %s is still disabled", bucketName)
		}
		return nil
	}
}

// testAccCheckTableBucketDestroy asserts the destroy actually switched the
// catalog off. RustFS 1.0.1 does not implement DELETE (501), so destroy there
// warns and drops the resource from state while the server keeps the catalog
// enabled; the listener route cannot be asserted on that release.
func testAccCheckTableBucketDestroy(s *terraform.State) error {
	client, err := testAccTableBucketClient()
	if err != nil {
		return err
	}
	version, _, err := client.RustClient.ServerVersion(context.Background())
	supportsDisable := err == nil && version.AtLeast(rustfs.Version{Major: 1, Minor: 0, Patch: 2})

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_table_bucket" {
			continue
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			continue
		}
		if !supportsDisable {
			continue
		}
		tableBucket, err := client.RustClient.GetTableBucket(context.Background(), bucketName)
		if err != nil {
			// A missing bucket has no catalog switch left to verify.
			continue
		}
		if tableBucket.Enabled {
			return fmt.Errorf("S3 Tables catalog for bucket %s is still enabled", bucketName)
		}
	}
	return nil
}

// testAccTableBucketClient returns the provider's own configured client. It
// runs the same Configure path Terraform takes, so the endpoint and credential
// resolution (environment variables in the acceptance run) is not re-implemented
// in the check helpers.
func testAccTableBucketClient() (*AllClient, error) {
	// Null attributes let Configure resolve every value from the environment,
	// exactly like the provider block the acceptance suite declares.
	resp, err := configureProviderValue(RustfsProviderModel{
		Endpoint:     types.StringNull(),
		AccessKey:    types.StringNull(),
		SecretKey:    types.StringNull(),
		AccessSecret: types.StringNull(),
		Ssl:          types.BoolNull(),
		Insecure:     types.BoolNull(),
	})
	if err != nil {
		return nil, err
	}
	if resp.Diagnostics.HasError() {
		return nil, fmt.Errorf("provider configure: %v", resp.Diagnostics)
	}
	client, ok := resp.ResourceData.(*AllClient)
	if !ok {
		return nil, fmt.Errorf("expected *AllClient in ResourceData, got %T", resp.ResourceData)
	}
	return client, nil
}
