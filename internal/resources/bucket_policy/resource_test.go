package bucket_policy_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/acceptance"
	rsbucketpolicy "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_policy"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestBucketPolicyResourceSchema(t *testing.T) {
	r := rsbucketpolicy.NewBucketPolicyResource()
	req := frameworkresource.SchemaRequest{}
	resp := frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), req, &resp)

	for _, want := range []string{"bucket", "id", "policy"} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
}

func TestAccBucketPolicyResource(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-policy-bucket-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_policy.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: acceptance.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: testAccBucketPolicyConfig(bucketName, "PublicRead", "s3:GetObject"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", bucketName),
					resource.TestCheckResourceAttr(resourceName, "id", bucketName),
					resource.TestCheckResourceAttrSet(resourceName, "policy"),
				),
			},
			{
				Config: testAccBucketPolicyConfig(bucketName, "WriteOnly", "s3:PutObject"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", bucketName),
					resource.TestCheckResourceAttrSet(resourceName, "policy"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     bucketName,
			},
		},
	})
}

func testAccBucketPolicyConfig(bucketName, sid, action string) string {
	return acceptance.ProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = %q
}

resource "rustfs_bucket_policy" "test" {
  bucket = rustfs_bucket.test.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = %q
      Effect    = "Allow"
      Principal = { AWS = ["*"] }
      Action    = [%q]
      Resource  = ["arn:aws:s3:::%s/*"]
    }]
  })
}
`, bucketName, sid, action, bucketName)
}
