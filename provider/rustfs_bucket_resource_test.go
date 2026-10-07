package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccBucketResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-bucket-%d", acctest.RandInt())
	resourceName := "rustfs_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", name),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
	})
}

func testAccBucketConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}
`, name)
}

func testAccCheckBucketExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}

		bucketName := rs.Primary.Attributes["name"]
		if bucketName == "" {
			return fmt.Errorf("no bucket name set")
		}

		client, err := testAccS3Client()
		if err != nil {
			return err
		}

		exists, err := bucketExists(context.Background(), client, bucketName)
		if err != nil {
			return fmt.Errorf("error checking bucket: %s", err)
		}
		if !exists {
			return fmt.Errorf("bucket %s does not exist", bucketName)
		}
		return nil
	}
}

func testAccCheckBucketDestroy(s *terraform.State) error {
	client, err := testAccS3Client()
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

		exists, err := bucketExists(context.Background(), client, bucketName)
		if err != nil {
			return fmt.Errorf("error checking bucket destruction: %s", err)
		}
		if exists {
			return fmt.Errorf("bucket %s still exists", bucketName)
		}
	}
	return nil
}

func testAccS3Client() (*s3.Client, error) {
	return newTestS3Client(os.Getenv("RUSTFS_ENDPOINT"), os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"))
}

func newTestS3Client(endpoint, accessKey, secretKey string) (*s3.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	endpointURL := endpoint
	if !strings.Contains(endpointURL, "://") {
		endpointURL = "http://" + endpointURL
	}

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
		o.UsePathStyle = true
	}), nil
}
