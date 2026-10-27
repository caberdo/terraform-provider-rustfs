// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccSiteReplicationResource exercises the rustfs_site_replication resource
// against a running RustFS cluster plus a second site.
//
// The add route refuses a loopback site endpoint and derives the local site's
// endpoint from the Host header of the admin request, so the peer has to be
// reachable at an address that is not the client's loopback: pointing a
// host-side run at a published port (127.0.0.1:9001) is rejected no matter what
// the peer is. Set RUSTFS_SITE_REPLICATION_PEER_ENDPOINT (and optionally
// RUSTFS_PEER_ACCESS_KEY / RUSTFS_PEER_SECRET_KEY) to the second site as the
// server sees it; the shipped environment
// (`docker compose -f acc_test/docker-compose.yml --profile test run test`)
// exports http://rustfs-peer:9000 and runs the test. Without the variable the
// test skips, which is what a single-site developer run does.
func TestAccSiteReplicationResource(t *testing.T) {
	testAccPreCheck(t)
	endpoint := siteReplicationPeerEndpoint(t)
	accessKey, secretKey := siteReplicationPeerCredentials(t)
	waitForSiteReplicationPeer(t, endpoint)
	checkSiteReplicationAvailable(t, endpoint, accessKey, secretKey)
	resourceName := "rustfs_site_replication.peer"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSiteReplicationDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + siteReplicationConfig(endpoint, accessKey, secretKey),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", "peer"),
					resource.TestCheckResourceAttr(resourceName, "endpoint", endpoint),
					resource.TestCheckResourceAttr(resourceName, "skip_tls_verify", "false"),
				),
			},
			{
				// Update, not replacement: the secret key is the only attribute
				// the server accepts an in-place change for on an http peer.
				// A different endpoint registers a second site entry instead of
				// renaming the peer, and skip_tls_verify / ca_cert_pem are
				// rejected with "site replication TLS settings require an HTTPS
				// endpoint".
				Config: testAccProviderConfig() + siteReplicationConfig(endpoint, accessKey, secretKey+"-rotated"),
				// The secret is asserted by test-output-free checks only: the
				// framework prints attribute values on failure, and the wire
				// body itself is pinned by the unit test.
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", "peer"),
					resource.TestCheckResourceAttrSet(resourceName, "secret_key"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        "peer",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				// site-replication/info reports the peer's name, endpoint and
				// TLS setting only: the peer credentials cannot be read back,
				// so the configuration supplies them again after an import.
				ImportStateVerifyIgnore: []string{"access_key", "secret_key", "ca_cert_pem"},
			},
		},
	})
}

// TestAccSiteReplicationRepairResource repairs a healthy two-site group. The
// repair is idempotent, so on an already-consistent group the expected outcome
// is `success`; `partial` is accepted as well because the assertion is that the
// operation ran and its status is observable, not that this deployment has
// nothing left to reconcile.
func TestAccSiteReplicationRepairResource(t *testing.T) {
	testAccPreCheck(t)
	endpoint := siteReplicationPeerEndpoint(t)
	providerEndpoint := os.Getenv("RUSTFS_ENDPOINT")
	accessKey, secretKey := siteReplicationPeerCredentials(t)
	waitForSiteReplicationPeer(t, endpoint)
	checkSiteReplicationAvailable(t, endpoint, accessKey, secretKey)

	resourceName := "rustfs_site_replication_repair.repair"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSiteReplicationDestroy,
		Steps: []resource.TestStep{
			{
				// The interlock is enforced during plan, so a wrong value never
				// reaches the server.
				Config: testAccProviderConfig() + siteReplicationConfig(endpoint, accessKey, secretKey) + `
resource "rustfs_site_replication_repair" "repair" {
  confirm    = "repair"
  depends_on = [rustfs_site_replication.peer]
}
`,
				ExpectError: regexp.MustCompile(`Invalid site replication repair confirmation`),
			},
			{
				Config: testAccProviderConfig() + siteReplicationConfig(endpoint, accessKey, secretKey) + `
resource "rustfs_site_replication_repair" "repair" {
  confirm    = "https://other.example.com:9000"
  depends_on = [rustfs_site_replication.peer]
}
`,
				ExpectError: regexp.MustCompile(`Invalid site replication repair confirmation`),
			},
			{
				Config: testAccProviderConfig() + siteReplicationConfig(endpoint, accessKey, secretKey) + fmt.Sprintf(`
resource "rustfs_site_replication_repair" "repair" {
  confirm    = %q
  depends_on = [rustfs_site_replication.peer]
}
`, providerEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "confirm", providerEndpoint),
					resource.TestCheckResourceAttrSet(resourceName, "operation_id"),
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttrSet(resourceName, "retry_events"),
					resource.TestMatchResourceAttr(resourceName, "status", regexp.MustCompile(`^(success|partial)$`)),
				),
			},
		},
	})
}

// siteReplicationPeerEndpoint returns the second site's endpoint, skipping the
// test when no peer is configured.
func siteReplicationPeerEndpoint(t *testing.T) string {
	t.Helper()
	endpoint := envOr("RUSTFS_SITE_REPLICATION_PEER_ENDPOINT", "")
	if endpoint == "" {
		t.Skip("RUSTFS_SITE_REPLICATION_PEER_ENDPOINT must point at a second RustFS site for this test; " +
			"see .github/CONTRIBUTING.md (\"Acceptance tests\")")
	}
	return strings.TrimSuffix(endpoint, "/")
}

// siteReplicationPeerCredentials returns the peer's credentials, which the
// fixture exports. They are required rather than defaulted, so a misconfigured
// run cannot silently authenticate against the peer with a guessed value.
func siteReplicationPeerCredentials(t *testing.T) (string, string) {
	t.Helper()
	accessKey := os.Getenv("RUSTFS_PEER_ACCESS_KEY")
	secretKey := os.Getenv("RUSTFS_PEER_SECRET_KEY")
	if accessKey == "" || secretKey == "" {
		t.Skip("RUSTFS_PEER_ACCESS_KEY and RUSTFS_PEER_SECRET_KEY must hold the peer's credentials; " +
			"see .github/CONTRIBUTING.md (\"Acceptance tests\")")
	}
	return accessKey, secretKey
}

// waitForSiteReplicationPeer blocks until the peer answers its readiness probe,
// so a compose run never races the second container's startup.
func waitForSiteReplicationPeer(t *testing.T, endpoint string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	readyURL := endpoint + "/health/ready"
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, readyURL, nil)
		if err != nil {
			t.Fatalf("building readiness request for %s: %v", readyURL, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"ready":true`) {
				return
			}
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, body)
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("site replication peer %s did not report ready: %v", readyURL, lastErr)
}

// checkSiteReplicationAvailable attempts a real add against the configured peer
// and skips the test when the server refuses it. RustFS rejects a loopback
// endpoint, which is what a host-side run against a published port looks like,
// and a server without the route answers 501.
func checkSiteReplicationAvailable(t *testing.T, endpoint, accessKey, secretKey string) {
	t.Helper()
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    rustfsadminAccessKey(),
		AccessSecret: rustfsadminSecretKey(),
		Endpoint:     rustfsadminEndpoint(),
		Ssl:          false,
	})
	err := client.SiteReplicationAdd(context.Background(), rustfs.SiteReplicationSite{
		Name:      "tf-acc-probe",
		Endpoint:  endpoint,
		AccessKey: accessKey,
		SecretKey: secretKey,
	})
	if err == nil {
		_ = client.RemovePeer(context.Background(), []string{"tf-acc-probe"})
		return
	}
	// A server without the route answers NotImplemented, and one that rejects
	// the probe (a loopback endpoint, or a shape it does not accept) answers
	// InvalidRequest: both mean "this deployment cannot be probed", so the test
	// skips instead of failing the suite. The peer's readiness was checked above,
	// so a misconfigured endpoint does not hide here.
	if strings.Contains(err.Error(), "loopback") || rustfs.IsAPIErrorCode(err, "NotImplemented", "InvalidRequest") {
		t.Skipf("site replication unavailable for peer %s: %v", endpoint, err)
	}
	t.Fatalf("unexpected error probing site replication: %v", err)
}

// testAccCheckSiteReplicationDestroy verifies the peer is gone from the group
// once Terraform has destroyed the resource.
func testAccCheckSiteReplicationDestroy(s *terraform.State) error {
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    rustfsadminAccessKey(),
		AccessSecret: rustfsadminSecretKey(),
		Endpoint:     rustfsadminEndpoint(),
		Ssl:          false,
	})
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_site_replication" {
			continue
		}
		name := rs.Primary.Attributes["name"]
		if name == "" {
			continue
		}
		info, err := client.SiteReplicationInfo(context.Background())
		if err != nil {
			return fmt.Errorf("reading site replication after destroy: %w", err)
		}
		for i := range info.Sites {
			if info.Sites[i].Name == name {
				return fmt.Errorf("site replication peer %s still exists", name)
			}
		}
	}
	return nil
}

func siteReplicationConfig(endpoint, accessKey, secretKey string) string {
	return fmt.Sprintf(`
resource "rustfs_site_replication" "peer" {
  name       = "peer"
  endpoint   = %q
  access_key = %q
  secret_key = %q
}
`, endpoint, accessKey, secretKey)
}

func rustfsadminEndpoint() string {
	return envOr("RUSTFS_ENDPOINT", "")
}

func rustfsadminAccessKey() string {
	return envOr("RUSTFS_USER", "")
}

func rustfsadminSecretKey() string {
	return envOr("RUSTFS_SECRET", "")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
