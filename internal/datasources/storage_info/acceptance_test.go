package storage_info_test

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
)

func TestAccStorageInfo(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 acceptance.LivePreCheck(t),
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: acceptance.ProviderConfig() + `
data "rustfs_storage_info" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "raw_json"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "backend.backend_type"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "disks.#"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "disks.0.path"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "disks.0.total_space"),
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "disks.0.state"),
				),
			},
			{
				Config: acceptance.ProviderConfig() + `
data "rustfs_storage_info" "test" {}

output "storage_raw" {
  value = data.rustfs_storage_info.test.raw_json
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_storage_info.test", "raw_json"),
					resource.TestCheckResourceAttrWith("data.rustfs_storage_info.test", "raw_json", func(v string) error {
						var parsed map[string]interface{}
						if err := json.Unmarshal([]byte(v), &parsed); err != nil {
							return err
						}
						if _, ok := parsed["info"]; !ok {
							t.Errorf("raw_json missing info field")
						}
						return nil
					}),
				),
			},
		},
	})
}
