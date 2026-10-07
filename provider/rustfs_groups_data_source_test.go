package provider

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccGroupsDataSource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC must be set for acceptance tests")
	}
	groupName := fmt.Sprintf("tf-acc-groups-%d", time.Now().UnixNano())
	createAccTestGroup(t, groupName)
	defer deleteAccTestGroup(t, groupName)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_groups" "all" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_groups.all", "groups.#"),
					resource.TestCheckTypeSetElemAttr("data.rustfs_groups.all", "groups.*", groupName),
				),
			},
		},
	})
}

func createAccTestGroup(t *testing.T, name string) {
	t.Helper()
	client := testAccAdminClient()
	if err := client.UpdateGroupMembers(rustfs.GroupAddRemove{Group: name, Members: []string{}, Status: "enabled"}); err != nil {
		t.Fatalf("creating group %s: %v", name, err)
	}
}

func deleteAccTestGroup(t *testing.T, name string) {
	t.Helper()
	client := testAccAdminClient()
	if err := client.DeleteGroup(name); err != nil {
		t.Fatalf("deleting group %s: %v", name, err)
	}
}

func testAccAdminClient() rustfs.RustfsAdmin {
	return rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		Ssl:          false,
	})
}
