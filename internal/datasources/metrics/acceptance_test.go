package metrics_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
)

func TestAccMetricsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: acceptance.ProviderConfig() + `
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
