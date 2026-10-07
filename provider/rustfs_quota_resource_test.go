package provider

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccQuotaResource_basic(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-quota-%d", acctest.RandInt())
	resourceName := "rustfs_quota.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckQuotaAndBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccQuotaConfig(bucketName, 100000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportStateId:                        bucketName,
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func TestAccQuotaResource_update(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-quota-%d", acctest.RandInt())
	resourceName := "rustfs_quota.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckQuotaAndBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccQuotaConfig(bucketName, 100000),
				Check:  resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
			},
			{
				Config: testAccQuotaConfig(bucketName, 200000),
				Check:  resource.TestCheckResourceAttr(resourceName, "quota", "200000"),
			},
		},
	})
}

func testAccQuotaConfig(bucket string, quota int) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_quota" "test" {
  bucket     = rustfs_bucket.test.name
  quota      = %d
  depends_on = [rustfs_bucket.test]
}
`, bucket, quota)
}

func testAccCheckQuotaExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			return fmt.Errorf("no bucket set")
		}
		client := testAccQuotaClient()

		// Retry for eventual consistency: bucket usage may not be authoritative yet.
		maxRetries := 10
		for i := 0; i < maxRetries; i++ {
			_, err := client.ReadQuota(bucketName)
			if err == nil {
				return nil
			}
			if i == maxRetries-1 {
				return fmt.Errorf("quota not found after %d attempts: %s", maxRetries, err)
			}
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
		}
		return nil
	}
}

func testAccCheckQuotaAndBucketDestroy(s *terraform.State) error {
	client := testAccQuotaClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_quota" {
			continue
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			continue
		}
		_, err := client.ReadQuota(bucketName)
		if err == nil {
			return fmt.Errorf("quota for bucket %s still exists", bucketName)
		}
	}
	return nil
}

func testAccQuotaClient() rustfs.RustfsAdmin {
	return rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}
