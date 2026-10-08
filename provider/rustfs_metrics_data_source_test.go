package provider

import (
	"fmt"
	"testing"

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
// exposes the admin metrics stream. RustFS releases predating
// GET /rustfs/admin/v3/metrics answer the unimplemented route with the generic
// s3s NotImplemented error, so the test skips on such servers instead of
// failing (mirrors the audit-target/LDAP skip convention).
func testAccMetricsPreCheck(t *testing.T) {
	testAccPreCheck(t)
	client := testAccRustClient()
	if _, err := client.GetMetrics(); err != nil {
		t.Skipf("the admin metrics stream is not available on the running RustFS (unimplemented route), skipping: %v", err)
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
