package kms_status_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
)

func TestAccKmsStatusDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 acceptance.LivePreCheck(t),
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: acceptance.ProviderConfig() + `
data "rustfs_kms_status" "current" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_kms_status.current", "backend_type"),
					resource.TestCheckResourceAttrSet("data.rustfs_kms_status.current", "backend_status"),
				),
			},
		},
	})
}
