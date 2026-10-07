package ilm_tier_stats_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
)

func TestAccIlmTierStatsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 acceptance.LivePreCheck(t),
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: acceptance.ProviderConfig() + `
data "rustfs_ilm_tier_stats" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_ilm_tier_stats.test", "id"),
				),
			},
		},
	})
}
