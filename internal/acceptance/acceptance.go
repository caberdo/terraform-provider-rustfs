package acceptance

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/weinmann-emt/terraform-provider-rustfs/provider"
)

// ProtoV6ProviderFactories returns the provider factories used by acceptance
// tests. It lives outside the provider package so tests in
// internal/datasources/<name> can configure the provider without an import
// cycle.
func ProtoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"rustfs": providerserver.NewProtocol6WithError(provider.New("test")()),
	}
}

// PreCheck skips the test unless TF_ACC is set and fails when the required
// RustFS connection environment variables are missing.
func PreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC must be set for acceptance tests")
	}
	for _, env := range []string{"RUSTFS_ENDPOINT", "RUSTFS_USER", "RUSTFS_SECRET"} {
		if os.Getenv(env) == "" {
			t.Fatalf("%s must be set for acceptance tests", env)
		}
	}
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
