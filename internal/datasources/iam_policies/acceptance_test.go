package iam_policies_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
)

func TestAccIAMPoliciesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: acceptance.ProviderConfig() + `
data "rustfs_iam_policies" "all" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_iam_policies.all", "policies.#"),
				),
			},
		},
	})
}
