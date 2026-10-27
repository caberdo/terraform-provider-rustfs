// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// siteReplicationAcceptancePeer resolves the site-replication peer named by
// RUSTFS_SITE_REPLICATION_PEER. The variable is required: without a peer the
// group is single-site, the status surface has nothing meaningful to report,
// and a resync has no target. A deployment that reports site replication
// disabled makes both tests skip with that reason instead of failing.
func siteReplicationAcceptancePeer(t *testing.T) (name, deploymentID string) {
	t.Helper()

	peer := strings.TrimSpace(os.Getenv("RUSTFS_SITE_REPLICATION_PEER"))
	if peer == "" {
		t.Skip("RUSTFS_SITE_REPLICATION_PEER must name a site-replication peer site for this test; " +
			"see .github/CONTRIBUTING.md (\"Acceptance tests\")")
	}

	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    rustfsadminAccessKey(),
		AccessSecret: rustfsadminSecretKey(),
		Endpoint:     rustfsadminEndpoint(),
		Ssl:          false,
	})

	info, err := client.SiteReplicationInfo(context.Background())
	if err != nil {
		t.Skipf("site-replication info is unavailable on this server: %v", err)
	}
	if !info.Enabled {
		t.Skipf("RUSTFS_SITE_REPLICATION_PEER is set to %q but the deployment reports site replication "+
			"disabled (enabled = false)", peer)
	}

	for _, site := range info.Sites {
		if site.Name == peer && site.DeploymentID != "" {
			return site.Name, site.DeploymentID
		}
	}

	status, err := client.SiteReplicationStatus(context.Background())
	if err != nil {
		t.Skipf("site-replication status is unavailable on this server: %v", err)
	}
	for _, site := range status.Sites {
		if site.Name == peer && site.DeploymentID != "" {
			return site.Name, site.DeploymentID
		}
	}

	t.Skipf("site-replication peer %q is not part of the configured group (%d sites reported): set "+
		"RUSTFS_SITE_REPLICATION_PEER to a peer name from rustfs_site_replication_status", peer, len(info.Sites))
	return "", ""
}

// checkSiteReplicationStatusListsPeer asserts the peer name appears in the
// data source's sites list. It reads the flat state attributes because a list
// index is not known ahead of the read.
func checkSiteReplicationStatusListsPeer(resourceName, peer string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		rs, ok := state.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("data source %s is not in state", resourceName)
		}
		for key, value := range rs.Primary.Attributes {
			if strings.HasPrefix(key, "sites.") && strings.HasSuffix(key, ".name") && value == peer {
				return nil
			}
		}
		return fmt.Errorf("sites list does not contain peer %q: %v", peer, rs.Primary.Attributes)
	}
}

func TestAccSiteReplicationStatusDataSource(t *testing.T) {
	testAccPreCheck(t)
	peer, _ := siteReplicationAcceptancePeer(t)

	dataSourceName := "data.rustfs_site_replication_status.status"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_site_replication_status" "status" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(dataSourceName, "enabled", "true"),
					resource.TestCheckResourceAttr(dataSourceName, "status_available", "true"),
					resource.TestCheckResourceAttrSet(dataSourceName, "name"),
					resource.TestCheckResourceAttrSet(dataSourceName, "deployment_id"),
					resource.TestCheckResourceAttrSet(dataSourceName, "metainfo.0.deployment_id"),
					resource.TestCheckResourceAttrWith(dataSourceName, "sites.#", func(value string) error {
						if value == "" || value == "0" {
							return fmt.Errorf("sites is empty for an enabled group")
						}
						return nil
					}),
					checkSiteReplicationStatusListsPeer(dataSourceName, peer),
				),
			},
		},
	})
}

// TestAccSiteReplicationResyncResource only ever runs operation = "status":
// starting a resync moves data between sites, which an acceptance run must not
// do. The test asserts the server answers an operation record; the recorded
// state is a not-found record when the peer has no resync history, which is a
// valid answer.
func TestAccSiteReplicationResyncResource(t *testing.T) {
	testAccPreCheck(t)
	peer, deploymentID := siteReplicationAcceptancePeer(t)

	resourceName := "rustfs_site_replication_resync.status"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_site_replication_resync" "status" {
  operation = "status"

  peer {
    name          = %q
    deployment_id = %q
  }
}
`, peer, deploymentID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "operation", "status"),
					resource.TestCheckResourceAttr(resourceName, "peer.name", peer),
					resource.TestCheckResourceAttr(resourceName, "peer.deployment_id", deploymentID),
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttr(resourceName, "op_type", "status"),
					resource.TestCheckResourceAttrSet(resourceName, "status"),
				),
			},
		},
	})
}
