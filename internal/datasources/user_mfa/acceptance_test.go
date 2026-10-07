package user_mfa_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
)

func TestAccUserMfaDataSource(t *testing.T) {
	name := fmt.Sprintf("tf-test-mfa-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: acceptance.ProviderConfig() + fmt.Sprintf(`
resource "rustfs_user" "test" {
  name       = "%s"
  access_key = "%s"
  secret_key = "superSecret123!"
  status     = "enabled"
  policy     = ""
}

data "rustfs_user_mfa" "test" {
  access_key = rustfs_user.test.access_key
}
`, name, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_user_mfa.test", "access_key", name),
					resource.TestCheckResourceAttrSet("data.rustfs_user_mfa.test", "enabled"),
				),
			},
		},
	})
}
