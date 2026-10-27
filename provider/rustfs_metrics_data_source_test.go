// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccMetricsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccMetricsPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_metrics" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_metrics.test", "metrics"),
					testCheckMetricsNonEmpty("data.rustfs_metrics.test"),
				),
			},
		},
	})
}

// testAccMetricsPreCheck verifies the environment and that the running RustFS
// implements the admin metrics stream. RustFS registers
// GET /rustfs/admin/v3/metrics before implementing it and answers the route
// with 501 NotImplemented, which the client reports as an
// *UnsupportedFeatureError; only that capability gap skips the test, so an
// unexpected failure still fails it.
func testAccMetricsPreCheck(t *testing.T) {
	testAccPreCheck(t)
	client := testAccRustClient()
	if _, err := client.GetMetrics(context.Background()); err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("the admin metrics stream is not implemented by the running RustFS, skipping: %v", err)
		}
		t.Fatalf("could not read the admin metrics stream: %v", err)
	}
}

func testCheckMetricsNonEmpty(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %s not found in state", resourceName)
		}
		if metrics, ok := rs.Primary.Attributes["metrics"]; !ok || metrics == "" {
			return fmt.Errorf("expected metrics attribute to be set and non-empty")
		}
		return nil
	}
}
