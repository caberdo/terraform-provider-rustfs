// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccTLSCertificate_basic publishes a certificate on a native-TLS RustFS
// listener, checks that the data source reports it, and asserts that an
// identical configuration is a no-op. Destroy cannot remove a published
// certificate because RustFS has no route for it, so CheckDestroy instead
// asserts the certificate the server kept serving is the one just published.
func TestAccTLSCertificate_basic(t *testing.T) {
	endpoint := os.Getenv("RUSTFS_TLS_ENDPOINT")
	if endpoint == "" {
		t.Skip("RUSTFS_TLS_ENDPOINT must be set to a native-TLS RustFS admin endpoint for this test")
	}

	client := testAccTLSCertificateClient(t, endpoint)
	if _, err := client.GetTLSCertificateState(context.Background()); err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("the running RustFS does not implement TLS certificate management, skipping: %v", err)
		}
		t.Fatalf("could not read the TLS certificate state: %v", err)
	}

	// The provider resolves endpoint/credentials from the environment before
	// the configuration, so point RUSTFS_ENDPOINT at this listener for the
	// duration of the test. resource.Test (not ParallelTest) keeps the process
	// environment private to this test.
	t.Setenv("RUSTFS_ENDPOINT", endpoint)

	certificatePEM := readTestTLSFixture(t, "renewed_cert.pem")
	keyPEM := readTestTLSFixture(t, "renewed_key.pem")
	fingerprint, ok := certificateFingerprintFromPEM(certificatePEM)
	if !ok {
		t.Fatal("the TLS acceptance fixture is not a parseable certificate")
	}

	resourceName := "rustfs_tls_certificate.test"
	dataSourceName := "data.rustfs_tls_certificate_state.test"
	var publishedFingerprint string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTLSCertificateDestroy(client, &publishedFingerprint),
		Steps: []resource.TestStep{
			{
				Config: testAccTLSCertificateConfig(endpoint, certificatePEM, keyPEM),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "id", "server-default"),
					resource.TestCheckResourceAttr(resourceName, "schema_version", rustfs.TLSPublishSchemaVersion),
					resource.TestCheckResourceAttr(resourceName, "resource_id", "server-default"),
					resource.TestCheckResourceAttr(resourceName, "certificate_fingerprint", fingerprint),
					resource.TestCheckResourceAttrSet(resourceName, "target_revision"),
					resource.TestCheckResourceAttrSet(resourceName, "not_after_unix"),
					resource.TestCheckResourceAttrSet(resourceName, "last_issuance_id"),
					resource.TestCheckResourceAttr(dataSourceName, "certificate_fingerprint", fingerprint),
					resource.TestCheckResourceAttrPair(dataSourceName, "target_revision", resourceName, "target_revision"),
					testAccWaitTLSCertificateServed(client, fingerprint),
					testAccCaptureTLSCertificateFingerprint(resourceName, &publishedFingerprint),
				),
			},
			{
				// Re-applying the identical PEM pair must not publish again.
				Config:             testAccTLSCertificateConfig(endpoint, certificatePEM, keyPEM),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func readTestTLSFixture(t *testing.T, name string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("..", "acc_test", "tls", name))
	if err != nil {
		t.Fatalf("read TLS acceptance fixture %s: %v", name, err)
	}
	return string(content)
}

func testAccTLSCertificateClient(t *testing.T, endpoint string) *rustfs.RustfsAdmin {
	t.Helper()

	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Endpoint:     endpoint,
		Ssl:          true,
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- fixture certificate
			},
		},
	})
	return &client
}

func testAccTLSCertificateConfig(endpoint, certificatePEM, keyPEM string) string {
	return fmt.Sprintf(`provider "rustfs" {
  endpoint   = %q
  access_key = %q
  secret_key = %q
  ssl        = true
  insecure   = true
}

resource "rustfs_tls_certificate" "test" {
  certificate_pem = <<-EOT
%s
EOT
  key_pem = <<-EOT
%s
EOT
}

data "rustfs_tls_certificate_state" "test" {
  depends_on = [rustfs_tls_certificate.test]
}
`, endpoint, os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"), certificatePEM, keyPEM)
}

func testAccCaptureTLSCertificateFingerprint(name string, dest *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("resource %s not found in state", name)
		}
		*dest = rs.Primary.Attributes["certificate_fingerprint"]
		if *dest == "" {
			return fmt.Errorf("resource %s has no certificate_fingerprint", name)
		}
		return nil
	}
}

// testAccCheckTLSCertificateDestroy asserts the resource left state and that
// the server still serves the certificate it published, because destroy has no
// route to unpublish it.
func testAccCheckTLSCertificateDestroy(client *rustfs.RustfsAdmin, published *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if *published == "" {
			return nil
		}

		certificateState, err := client.GetTLSCertificateState(context.Background())
		if err != nil {
			return fmt.Errorf("read the served TLS certificate after destroy: %w", err)
		}
		if certificateState.CertificateFingerprint != *published {
			return fmt.Errorf("destroy changed the served certificate: published %s, server serves %s",
				*published, certificateState.CertificateFingerprint)
		}
		return nil
	}
}

// testAccWaitTLSCertificateServed waits until the listener reports the
// published certificate as the one it serves. RustFS reloads TLS material on
// its own interval, so observed_certificate_fingerprint can lag publication.
func testAccWaitTLSCertificateServed(client *rustfs.RustfsAdmin, fingerprint string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		deadline := time.Now().Add(30 * time.Second)
		for {
			certificateState, err := client.GetTLSCertificateState(context.Background())
			if err == nil && certificateState.ObservedCertificateFingerprint != nil &&
				*certificateState.ObservedCertificateFingerprint == fingerprint {
				return nil
			}
			if time.Now().After(deadline) {
				if err != nil {
					return fmt.Errorf("read the served TLS certificate: %w", err)
				}
				return fmt.Errorf("the listener did not serve certificate %s: observed %v",
					fingerprint, certificateState.ObservedCertificateFingerprint)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
}
