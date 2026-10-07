package quota_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
)

func TestAccQuotaDataSource(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-quota-ds-%d", acctest.RandInt())
	resourceName := "rustfs_quota.test"
	dataSourceName := "data.rustfs_quota.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 acceptance.LivePreCheck(t),
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		CheckDestroy:             checkQuotaAndBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccQuotaDataSourceConfig(bucketName, 250000),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair(dataSourceName, "bucket", resourceName, "bucket"),
					resource.TestCheckResourceAttr(dataSourceName, "quota", "250000"),
					resource.TestCheckResourceAttr(dataSourceName, "quota_type", "HARD"),
				),
			},
		},
	})
}

func testAccQuotaDataSourceConfig(bucket string, quota int) string {
	return acceptance.ProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_quota" "test" {
  bucket     = rustfs_bucket.test.name
  quota      = %d
  depends_on = [rustfs_bucket.test]
}

data "rustfs_quota" "test" {
  bucket = rustfs_quota.test.bucket
}
`, bucket, quota)
}

func checkQuotaAndBucketDestroy(s *terraform.State) error {
	c := client.New(&client.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_quota" {
			continue
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			continue
		}
		if _, err := c.ReadQuota(bucketName); err == nil {
			return fmt.Errorf("quota for bucket %s still exists", bucketName)
		}
	}
	return nil
}
