package server_info_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
)

func TestAccServerInfoDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: acceptance.ProviderConfig() + `
data "rustfs_server_info" "cluster" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_server_info.cluster", "mode"),
					resource.TestCheckResourceAttrSet("data.rustfs_server_info.cluster", "deployment_id"),
					resource.TestCheckResourceAttrSet("data.rustfs_server_info.cluster", "backend_type"),
					resource.TestCheckResourceAttrSet("data.rustfs_server_info.cluster", "servers.#"),
					resource.TestCheckResourceAttrSet("data.rustfs_server_info.cluster", "raw_json"),
				),
			},
		},
	})
}
