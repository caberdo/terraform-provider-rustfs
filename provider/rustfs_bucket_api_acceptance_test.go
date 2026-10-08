package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// skipUnlessEnv skips a test unless the named environment variable is set. It is
// used for tests that need server-side prerequisites (a registered remote
// target) that the default acceptance harness does not provision.
func skipUnlessEnv(t *testing.T, name, reason string) {
	t.Helper()
	if os.Getenv(name) == "" {
		t.Skipf("%s not set: %s", name, reason)
	}
}

func TestAccBucketVersioningResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-ver-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_versioning.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccVersioningConfig(name, "Enabled"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "status", "Enabled"),
				),
			},
			{
				Config: testAccVersioningConfig(name, "Suspended"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "status", "Suspended"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func testAccVersioningConfig(bucket, status string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_versioning" "test" {
  bucket = rustfs_bucket.test.name
  status = "%s"
}
`, bucket, status)
}

func TestAccBucketEncryptionResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-enc-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_encryption.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccEncryptionConfig(name, "AES256", ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.sse_algorithm", "AES256"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func testAccEncryptionConfig(bucket, algorithm, kmsKey string) string {
	kmsLine := ""
	if kmsKey != "" {
		kmsLine = fmt.Sprintf("\n        kms_master_key_id = %q", kmsKey)
	}
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_encryption" "test" {
  bucket = rustfs_bucket.test.name

  rule = [
    {
      apply_server_side_encryption_by_default = {
        sse_algorithm = "%s"%s
      }
    }
  ]
}
`, bucket, algorithm, kmsLine)
}

func TestAccBucketObjectLockResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-lock-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_object_lock.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccObjectLockConfig(name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "object_lock_enabled", "Enabled"),
					resource.TestCheckResourceAttr(resourceName, "rule.default_retention.mode", "GOVERNANCE"),
					resource.TestCheckResourceAttr(resourceName, "rule.default_retention.days", "1"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func testAccObjectLockConfig(bucket string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name                           = "%s"
  object_lock_enabled_for_bucket = true
}

resource "rustfs_bucket_object_lock" "test" {
  bucket              = rustfs_bucket.test.name
  object_lock_enabled = "Enabled"

  rule = {
    default_retention = {
      mode = "GOVERNANCE"
      days = 1
    }
  }
}
`, bucket)
}

func TestAccBucketEncryptionResource_kms(t *testing.T) {
	name := fmt.Sprintf("tf-test-enckms-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_encryption.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccEncryptionConfig(name, "aws:kms", "rustfs-default-key"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.sse_algorithm", "aws:kms"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.kms_master_key_id", "rustfs-default-key"),
				),
			},
		},
	})
}

func TestAccBucketResource_full(t *testing.T) {
	name := fmt.Sprintf("tf-test-bucket-full-%d", acctest.RandInt())
	resourceName := "rustfs_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketFullConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "object_ownership", "BucketOwnerEnforced"),
					resource.TestCheckResourceAttr(resourceName, "create_bucket_configuration.tags.env", "test"),
				),
			},
		},
	})
}

func testAccBucketFullConfig(bucket string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name             = "%s"
  acl              = "private"
  object_ownership = "BucketOwnerEnforced"

  create_bucket_configuration = {
    tags = {
      env = "test"
    }
  }
}
`, bucket)
}

func TestAccBucketNotificationResource_basic(t *testing.T) {
	skipUnlessEnv(t, "RUSTFS_TEST_REMOTE_TARGET", "RustFS rejects notification configs unless the target ARN references a registered notification target (ARN form ID:Name)")
	name := fmt.Sprintf("tf-test-notif-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_notification.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccNotificationConfig(name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "queue.0.arn", "arn:minio:sqs::PRIMARY:amqp"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.filter.prefix", "uploads/"),
				),
			},
		},
	})
}

func testAccNotificationConfig(bucket string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_notification" "test" {
  bucket = rustfs_bucket.test.name

  queue = [
    {
      arn    = "arn:minio:sqs::PRIMARY:amqp"
      events = ["s3:ObjectCreated:*", "s3:ObjectRemoved:*"]
      filter = {
        prefix = "uploads/"
        suffix = ".jpg"
      }
    }
  ]
}
`, bucket)
}

func TestAccBucketReplicationResource_basic(t *testing.T) {
	skipUnlessEnv(t, "RUSTFS_TEST_REMOTE_TARGET", "replication requires a registered remote target (otherwise 'replication config has a stale target') plus versioned source and destination buckets")
	name := fmt.Sprintf("tf-test-repl-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_replication.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccReplicationConfig(name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "role", "arn:minio:replication::test:dest"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.status", "Enabled"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.destination.bucket", fmt.Sprintf("arn:aws:s3:::%s-dst", name)),
				),
			},
		},
	})
}

func testAccReplicationConfig(bucket string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "src" {
  name = "%s-src"
}

resource "rustfs_bucket" "dst" {
  name = "%s-dst"
}

resource "rustfs_bucket_versioning" "src" {
  bucket = rustfs_bucket.src.name
  status = "Enabled"
}

resource "rustfs_bucket_versioning" "dst" {
  bucket = rustfs_bucket.dst.name
  status = "Enabled"
}

resource "rustfs_bucket_replication" "test" {
  bucket = rustfs_bucket.src.name
  role   = "arn:minio:replication::test:dest"

  depends_on = [rustfs_bucket_versioning.src, rustfs_bucket_versioning.dst]

  rule = [
    {
      status = "Enabled"
      destination = {
        bucket = "arn:aws:s3:::%s-dst"
      }
    }
  ]
}
`, bucket, bucket, bucket)
}
