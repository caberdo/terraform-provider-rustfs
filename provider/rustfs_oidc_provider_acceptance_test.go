// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccOIDCProvider_basic needs a real OIDC issuer the RustFS server can
// reach, because the resource runs discovery before saving. Set
// RUSTFS_OIDC_CONFIG_URL and RUSTFS_OIDC_CLIENT_ID (and optionally
// RUSTFS_OIDC_CLIENT_SECRET); without them the test skips, matching a server
// that has no OIDC backend to exercise.
func TestAccOIDCProvider_basic(t *testing.T) {
	configURL := os.Getenv("RUSTFS_OIDC_CONFIG_URL")
	clientID := os.Getenv("RUSTFS_OIDC_CLIENT_ID")
	if configURL == "" || clientID == "" {
		t.Skip("RUSTFS_OIDC_CONFIG_URL and RUSTFS_OIDC_CLIENT_ID are required for OIDC acceptance tests")
	}
	clientSecret := os.Getenv("RUSTFS_OIDC_CLIENT_SECRET")

	providerID := fmt.Sprintf("tf-oidc-%d", acctest.RandInt())
	resourceName := "rustfs_oidc_provider.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckOIDCProviderDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccOIDCProviderConfig(providerID, configURL, clientID, clientSecret, "Terraform OIDC"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckOIDCProviderExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "id", providerID),
					resource.TestCheckResourceAttr(resourceName, "display_name", "Terraform OIDC"),
					resource.TestCheckResourceAttr(resourceName, "enabled", "true"),
					resource.TestCheckResourceAttr(resourceName, "scopes.#", "3"),
					resource.TestCheckResourceAttr(resourceName, "source", "persisted"),
					resource.TestCheckResourceAttr(resourceName, "editable", "true"),
					resource.TestCheckResourceAttr(resourceName, "client_secret_configured", fmt.Sprintf("%t", clientSecret != "")),
					resource.TestCheckResourceAttr("data.rustfs_oidc_config.test", "providers.0.provider_id", providerID),
					resource.TestCheckResourceAttr("data.rustfs_oidc_config.test", "providers.0.client_secret_configured", fmt.Sprintf("%t", clientSecret != "")),
				),
			},
			{
				Config: testAccOIDCProviderConfig(providerID, configURL, clientID, clientSecret, "Terraform OIDC Updated"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckOIDCProviderExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "display_name", "Terraform OIDC Updated"),
				),
			},
			{
				// Computed consistency: the post-apply plan must be empty.
				Config:             testAccOIDCProviderConfig(providerID, configURL, clientID, clientSecret, "Terraform OIDC Updated"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:            resourceName,
				ImportStateId:           providerID,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"client_secret"},
			},
		},
	})
}

func testAccOIDCProviderConfig(providerID, configURL, clientID, clientSecret, displayName string) string {
	secretLine := ""
	if clientSecret != "" {
		secretLine = fmt.Sprintf("  client_secret = %q\n", clientSecret)
	}
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_oidc_provider" "test" {
  id           = %q
  config_url   = %q
  client_id    = %q
%s  display_name = %q
}

data "rustfs_oidc_config" "test" {
  provider_id = rustfs_oidc_provider.test.id
}
`, providerID, configURL, clientID, secretLine, displayName)
}

func testAccCheckOIDCProviderExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		client := testAccRustClient()
		providerID := rs.Primary.Attributes["id"]
		_, found, err := client.GetOIDCProvider(context.Background(), providerID)
		if err != nil {
			return fmt.Errorf("reading OIDC provider %s: %w", providerID, err)
		}
		if !found {
			return fmt.Errorf("OIDC provider %s does not exist", providerID)
		}
		return nil
	}
}

func testAccCheckOIDCProviderDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_oidc_provider" {
			continue
		}
		providerID := rs.Primary.Attributes["id"]
		_, found, err := client.GetOIDCProvider(context.Background(), providerID)
		if err != nil {
			return fmt.Errorf("reading OIDC provider %s: %w", providerID, err)
		}
		if found {
			return fmt.Errorf("OIDC provider %s still exists", providerID)
		}
	}
	return nil
}
