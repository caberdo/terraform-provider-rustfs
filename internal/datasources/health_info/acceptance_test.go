package health_info_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
)

func TestAccHealthInfoDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: acceptance.ProviderConfig() + `
data "rustfs_health_info" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_health_info.test", "health_info"),
					resource.TestCheckResourceAttrSet("data.rustfs_health_info.test", "obd_info"),
					resource.TestCheckResourceAttrSet("data.rustfs_health_info.test", "version"),
					resource.TestCheckResourceAttrSet("data.rustfs_health_info.test", "region"),
					resource.TestCheckResourceAttrSet("data.rustfs_health_info.test", "timestamp"),
				),
			},
		},
	})
}
