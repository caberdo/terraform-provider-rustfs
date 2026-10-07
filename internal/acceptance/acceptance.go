// Package acceptance provides the shared harness for the provider's live
// acceptance tests. It lives outside the provider package so tests in
// internal/datasources/<name> and internal/resources/<name> can configure the
// provider without an import cycle.
package acceptance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

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

// EnvOrDefault returns the value of the environment variable envKey, or
// defaultValue when it is unset.
func EnvOrDefault(envKey, defaultValue string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultValue
}

// MinioClient builds an S3 client from the acceptance environment.
func MinioClient() (*minio.Client, error) {
	return minio.New(os.Getenv("RUSTFS_ENDPOINT"), &minio.Options{
		Creds:  credentials.NewStaticV4(os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"), ""),
		Secure: false,
	})
}

// RustClient builds a RustFS admin client from the acceptance environment.
func RustClient() client.RustfsAdmin {
	return client.New(&client.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}

// CheckBucketDestroy asserts that every rustfs_bucket in state no longer
// exists in the S3 API.
func CheckBucketDestroy(s *terraform.State) error {
	c, err := MinioClient()
	if err != nil {
		return err
	}

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_bucket" {
			continue
		}

		bucketName := rs.Primary.Attributes["name"]
		if bucketName == "" {
			continue
		}

		exists, err := c.BucketExists(context.Background(), bucketName)
		if err != nil {
			minioErr, ok := err.(minio.ErrorResponse)
			if ok && (strings.Contains(minioErr.Code, "NotFound") || minioErr.StatusCode == 404) {
				continue
			}
			return fmt.Errorf("error checking bucket destruction: %s", err)
		}
		if exists {
			return fmt.Errorf("bucket %s still exists", bucketName)
		}
	}
	return nil
}
