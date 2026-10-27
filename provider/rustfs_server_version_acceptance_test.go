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

// TestAccServerVersion_supported records the version of the RustFS server under
// test and whether it implements the admin metrics route. It fails only when
// the server cannot report a version; a missing metrics route is a recorded
// capability gap, not a test failure, so the version matrix documentation stays
// backed by evidence from every server the suite runs against.
func TestAccServerVersion_supported(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig(),
				Check:  testAccCheckServerCapabilities(t),
			},
		},
	})
}

func testAccCheckServerCapabilities(t *testing.T) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		client := testAccRustClient()

		version, raw, err := client.ServerVersion(context.Background())
		if err != nil {
			return fmt.Errorf("could not determine the RustFS server version: %w", err)
		}
		t.Logf("RustFS server version: %s (parsed %s)", raw, version)

		if _, err := client.GetMetrics(context.Background()); err != nil {
			if !rustfs.IsUnsupportedFeature(err) {
				return fmt.Errorf("GET /rustfs/admin/v3/metrics failed unexpectedly: %w", err)
			}
			t.Logf("metrics: NOT available on %s: %v", version, err)
			return nil
		}
		t.Logf("metrics: available on %s", version)
		return nil
	}
}
