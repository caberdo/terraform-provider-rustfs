package replication_metrics_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
)

func TestAccReplicationMetrics(t *testing.T) {
	name := fmt.Sprintf("tf-test-repl-metrics-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: testAccReplicationMetricsConfig(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_replication_metrics.test", "bucket", name),
					resource.TestCheckResourceAttrSet("data.rustfs_replication_metrics.test", "id"),
					resource.TestCheckResourceAttrSet("data.rustfs_replication_metrics.test", "json"),
					resource.TestCheckResourceAttr("data.rustfs_replication_metrics.test", "replication_count", "0"),
				),
			},
		},
	})
}

func testAccReplicationMetricsConfig(name string) string {
	return acceptance.ProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

data "rustfs_replication_metrics" "test" {
  bucket = rustfs_bucket.test.name
}
`, name)
}
