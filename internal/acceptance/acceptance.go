// Package acceptance provides the shared harness for the provider's live
// acceptance tests. It lives outside the provider package so tests in
// internal/datasources/<name> (and, once #10 lands, internal/resources/<name>)
// can configure the provider without an import cycle.
package acceptance

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
	"github.com/weinmann-emt/terraform-provider-rustfs/provider"
)

// ProtoV6ProviderFactories returns the provider factories used by acceptance
// tests.
func ProtoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"rustfs": providerserver.NewProtocol6WithError(provider.New("test")()),
	}
}

// LivePreCheck returns a PreCheck func for live acceptance tests. It skips the
// test when TF_ACC is unset or RUSTFS_ENDPOINT is empty, and fails when the
// configured server is unreachable or rejects the configured credentials, so a
// live suite never silently passes against a mock or a dead endpoint.
func LivePreCheck(t *testing.T) func() {
	return func() {
		t.Helper()

		if os.Getenv("TF_ACC") == "" {
			t.Skip("TF_ACC must be set for acceptance tests")
		}

		endpoint := strings.TrimRight(os.Getenv("RUSTFS_ENDPOINT"), "/")
		if endpoint == "" {
			t.Skip("RUSTFS_ENDPOINT is not set; skipping live acceptance tests")
		}

		accessKey := os.Getenv("RUSTFS_USER")
		secretKey := os.Getenv("RUSTFS_SECRET")
		if accessKey == "" || secretKey == "" {
			t.Fatalf("RUSTFS_USER and RUSTFS_SECRET must be set for live acceptance tests")
		}

		c := client.New(&client.RustfsAdminConfig{
			AccessKey:    accessKey,
			AccessSecret: secretKey,
			Endpoint:     endpoint,
			Ssl:          strings.EqualFold(os.Getenv("RUSTFS_SSL"), "true"),
			Insecure:     strings.EqualFold(os.Getenv("RUSTFS_INSECURE"), "true"),
		})
		info, err := c.GetHealthInfo()
		if err != nil {
			t.Fatalf("server at %s is not reachable or rejected the credentials: %v", endpoint, err)
		}
		if info == nil || info.Version == "" {
			t.Fatalf("server at %s returned no version; refusing to run acceptance tests against a mock", endpoint)
		}
	}
}

// PreCheck is retained for callers that prefer not to build a PreCheck closure
// themselves; it skips unless TF_ACC and RUSTFS_ENDPOINT are set and fails when
// the server is unreachable.
func PreCheck(t *testing.T) {
	LivePreCheck(t)()
}

// ProviderConfig returns the provider block used by acceptance test configs.
func ProviderConfig() string {
	return `provider "rustfs" {
  endpoint   = "` + os.Getenv("RUSTFS_ENDPOINT") + `"
  access_key = "` + os.Getenv("RUSTFS_USER") + `"
  secret_key = "` + os.Getenv("RUSTFS_SECRET") + `"
  ssl        = false
}
`
}

// UniqueName returns a collision-free identifier prefixed with prefix, suitable
// for resources that share a live server across parallel tests.
func UniqueName(prefix string) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return fmt.Sprintf("acc_%s_%s", prefix, hex.EncodeToString(b))
}
