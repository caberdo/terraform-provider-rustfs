// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

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
					testAccCheckBucketVersioningOnServer(t, name, "Enabled"),
				),
			},
			{
				// In-place update: the versioning status is mutable, so this
				// exercises Update and asserts the value the server returned.
				Config: testAccVersioningConfig(name, "Suspended"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "status", "Suspended"),
					testAccCheckBucketVersioningOnServer(t, name, "Suspended"),
				),
			},
			{
				// Explicit computed consistency: the post-apply plan for the
				// configuration just applied must be empty.
				Config:             testAccVersioningConfig(name, "Suspended"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: re-enable versioning behind Terraform's
				// back. The mutation runs in Check (before the post-apply plan),
				// so the plan Terraform proposes is non-empty.
				Config:             testAccVersioningConfig(name, "Suspended"),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketVersioningOnServer(t, name, "Suspended"),
					testAccMutateBucketVersioningOutOfBand(t, name, "Enabled"),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				// The drifted value converges back to the configuration.
				Config: testAccVersioningConfig(name, "Suspended"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "status", "Suspended"),
					testAccCheckBucketVersioningOnServer(t, name, "Suspended"),
				),
			},
			{
				// Revert and read the original value back from the server.
				Config: testAccVersioningConfig(name, "Enabled"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "status", "Enabled"),
					testAccCheckBucketVersioningOnServer(t, name, "Enabled"),
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
					testAccCheckBucketEncryptionOnServer(t, name, "AES256", ""),
				),
			},
			{
				// In-place update: the SSE rule is mutable, so changing the
				// algorithm exercises Update and asserts the server value.
				Config: testAccEncryptionConfig(name, "aws:kms", "rustfs-default-key"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.sse_algorithm", "aws:kms"),
					testAccCheckBucketEncryptionOnServer(t, name, "aws:kms", "rustfs-default-key"),
				),
			},
			{
				// Revert to the original rule and read it back from the server.
				Config: testAccEncryptionConfig(name, "AES256", ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.sse_algorithm", "AES256"),
					testAccCheckBucketEncryptionOnServer(t, name, "AES256", ""),
				),
			},
			{
				// Explicit computed consistency: the post-apply plan for the
				// configuration just applied must be empty.
				Config:             testAccEncryptionConfig(name, "AES256", ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: switch the server-side encryption rule
				// behind Terraform's back; the proposed plan must be non-empty.
				Config:             testAccEncryptionConfig(name, "AES256", ""),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketEncryptionOnServer(t, name, "AES256", ""),
					testAccMutateBucketEncryptionOutOfBand(t, name, "aws:kms", "rustfs-default-key"),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				Config: testAccEncryptionConfig(name, "AES256", ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketEncryptionOnServer(t, name, "AES256", ""),
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
				Config: testAccObjectLockConfig(name, 1),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "object_lock_enabled", "Enabled"),
					resource.TestCheckResourceAttr(resourceName, "rule.default_retention.mode", "GOVERNANCE"),
					resource.TestCheckResourceAttr(resourceName, "rule.default_retention.days", "1"),
					testAccCheckBucketObjectLockOnServer(t, name, "GOVERNANCE", 1),
				),
			},
			{
				// In-place update: the default retention period is mutable, so
				// changing it exercises Update and asserts the server value.
				Config: testAccObjectLockConfig(name, 2),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.default_retention.days", "2"),
					testAccCheckBucketObjectLockOnServer(t, name, "GOVERNANCE", 2),
				),
			},
			{
				// Revert and read the original retention back from the server.
				Config: testAccObjectLockConfig(name, 1),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.default_retention.days", "1"),
					testAccCheckBucketObjectLockOnServer(t, name, "GOVERNANCE", 1),
				),
			},
			{
				// Explicit computed consistency: the post-apply plan for the
				// configuration just applied must be empty.
				Config:             testAccObjectLockConfig(name, 1),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: extend the default retention on the server
				// behind Terraform's back; the proposed plan must be non-empty.
				Config:             testAccObjectLockConfig(name, 1),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketObjectLockOnServer(t, name, "GOVERNANCE", 1),
					testAccMutateBucketObjectLockOutOfBand(t, name, 5),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				Config: testAccObjectLockConfig(name, 1),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketObjectLockOnServer(t, name, "GOVERNANCE", 1),
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

func testAccObjectLockConfig(bucket string, days int) string {
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
      days = %d
    }
  }
}
`, bucket, days)
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
					testAccCheckBucketEncryptionOnServer(t, name, "aws:kms", "rustfs-default-key"),
				),
			},
			{
				// In-place update away from SSE-KMS and back, asserting the
				// server-side value after each change.
				Config: testAccEncryptionConfig(name, "AES256", ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.sse_algorithm", "AES256"),
					testAccCheckBucketEncryptionOnServer(t, name, "AES256", ""),
				),
			},
			{
				Config: testAccEncryptionConfig(name, "aws:kms", "rustfs-default-key"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.sse_algorithm", "aws:kms"),
					testAccCheckBucketEncryptionOnServer(t, name, "aws:kms", "rustfs-default-key"),
				),
			},
			{
				Config:             testAccEncryptionConfig(name, "aws:kms", "rustfs-default-key"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
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

func TestAccBucketResource_full(t *testing.T) {
	name := fmt.Sprintf("tf-test-bucket-full-%d", acctest.RandInt())
	resourceName := "rustfs_bucket.test"

	// object_ownership is the only in-place mutable attribute on this resource,
	// so it is the only route into Update. RustFS 1.0.3 answers both
	// GetBucketOwnershipControls and PutBucketOwnershipControls with 501
	// NotImplemented, so on that server the update path is exercised through
	// the diagnostic it forces; on a server that implements the route the real
	// update and its read-back are asserted instead.
	ownershipControlsAvailable := testAccBucketOwnershipControlsAvailable(t)

	steps := []resource.TestStep{
		{
			// RustFS 1.0.3 does not persist CreateBucketConfiguration.TagSet
			// (GetBucketTagging answers NoSuchTagSet for the fresh bucket), so
			// the creation-time tag is asserted through Terraform state here;
			// the server-side tag set is exercised by the drift step below.
			Config: testAccBucketFullConfig(name, "BucketOwnerEnforced", "test"),
			Check: resource.ComposeTestCheckFunc(
				testAccCheckBucketExists(resourceName),
				resource.TestCheckResourceAttr(resourceName, "object_ownership", "BucketOwnerEnforced"),
				resource.TestCheckResourceAttr(resourceName, "create_bucket_configuration.tags.env", "test"),
			),
		},
	}

	if ownershipControlsAvailable {
		steps = append(steps,
			resource.TestStep{
				// In-place update: object_ownership is the only mutable
				// attribute, and this asserts the server accepted the change.
				Config: testAccBucketFullConfig(name, "BucketOwnerPreferred", "test"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "object_ownership", "BucketOwnerPreferred"),
					testAccCheckBucketOwnershipOnServer(t, name, "BucketOwnerPreferred"),
				),
			},
			resource.TestStep{
				// Revert and read the original ownership back from the server.
				Config: testAccBucketFullConfig(name, "BucketOwnerEnforced", "test"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "object_ownership", "BucketOwnerEnforced"),
					testAccCheckBucketOwnershipOnServer(t, name, "BucketOwnerEnforced"),
				),
			},
		)
	} else {
		steps = append(steps,
			resource.TestStep{
				// The server cannot apply an ownership change, so Update is
				// entered and observed through the diagnostic it returns.
				Config:      testAccBucketFullConfig(name, "BucketOwnerPreferred", "test"),
				ExpectError: regexp.MustCompile(`Error updating bucket object ownership`),
			},
			resource.TestStep{
				// The failed update left the prior ownership in state, so this
				// configuration plans nothing and the bucket is intact.
				Config: testAccBucketFullConfig(name, "BucketOwnerEnforced", "test"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "object_ownership", "BucketOwnerEnforced"),
				),
			},
		)
	}

	steps = append(steps,
		resource.TestStep{
			// Explicit computed consistency: the post-apply plan for the
			// configuration just applied must be empty.
			Config:             testAccBucketFullConfig(name, "BucketOwnerEnforced", "test"),
			PlanOnly:           true,
			ExpectNonEmptyPlan: false,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
		},
		resource.TestStep{
			// Replacement plan: create_bucket_configuration.tags carries
			// RequiresReplace(), so changing a tag must plan a replacement.
			Config:             testAccBucketFullConfig(name, "BucketOwnerEnforced", "prod"),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPreRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
				},
			},
		},
		resource.TestStep{
			// Apply the replacement so the replacement path really runs and the
			// substitute bucket exists on the server.
			Config: testAccBucketFullConfig(name, "BucketOwnerEnforced", "prod"),
			Check: resource.ComposeTestCheckFunc(
				testAccCheckBucketExists(resourceName),
				resource.TestCheckResourceAttr(resourceName, "name", name),
			),
		},
		resource.TestStep{
			// acl is mutable: changing it must update the bucket in place, not
			// replace it (a replacement would delete every object).
			Config:             testAccBucketFullConfigWithACL(name, "BucketOwnerEnforced", "prod", "public-read"),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPreRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
				},
			},
		},
		resource.TestStep{
			// Out-of-band drift: retag the bucket behind Terraform's back. Tags
			// are refreshed on read and carry RequiresReplace(), so the plan
			// Terraform proposes must be a replacement.
			Config:             testAccBucketFullConfig(name, "BucketOwnerEnforced", "prod"),
			ExpectNonEmptyPlan: true,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckBucketExists(resourceName),
				testAccMutateBucketTagOutOfBand(t, name, "drifted"),
				testAccCheckBucketTagOnServer(t, name, "env", "drifted"),
			),
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPostRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
				},
			},
		},
		resource.TestStep{
			// The drift converges back to the configured tag; the server has no
			// tag set for the replacement bucket, which is fine because the
			// applied state carries the configured value.
			Config: testAccBucketFullConfig(name, "BucketOwnerEnforced", "prod"),
			Check: resource.ComposeTestCheckFunc(
				testAccCheckBucketExists(resourceName),
				resource.TestCheckResourceAttr(resourceName, "create_bucket_configuration.tags.env", "prod"),
			),
		},
	)

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps:                    steps,
	})
}

func testAccBucketFullConfig(bucket, ownership, tagValue string) string {
	return testAccBucketFullConfigWithACL(bucket, ownership, tagValue, "private")
}

func testAccBucketFullConfigWithACL(bucket, ownership, tagValue, acl string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name             = "%s"
  acl              = "%s"
  object_ownership = "%s"

  create_bucket_configuration = {
    tags = {
      env = "%s"
    }
  }
}
`, bucket, acl, ownership, tagValue)
}

// bucketNotificationTargetID is the notification target the bucket
// notification acceptance test registers in the acceptance environment. Its
// ARN follows RustFS' own form, arn:rustfs:sqs:<region>:<id>:<service>; the
// legacy MinIO form (arn:minio:sqs::...) is rejected as an invalid target ID.
const bucketNotificationTargetID = "PRIMARY"

// testAccBucketNotificationPreCheck enables the notify module and registers the
// webhook target the notification configuration references. RustFS fails the
// PutBucketNotificationConfiguration call with "No notify targets configured"
// otherwise, so this is a fixture-level prerequisite rather than an optional
// extra.
func testAccBucketNotificationPreCheck(t *testing.T) {
	t.Helper()
	testAccPreCheck(t)

	endpoint := os.Getenv("RUSTFS_NOTIFY_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("RUSTFS_NOTIFY_TEST_ENDPOINT must be set for the bucket notification acceptance test")
	}
	testAccEnsureModuleEnabled(t, "notify")

	client := testAccRustClient()
	if err := client.SetNotificationTarget(context.Background(), "notify_webhook", bucketNotificationTargetID, []rustfs.TargetKeyValue{
		{Key: "endpoint", Value: endpoint},
	}); err != nil {
		if strings.Contains(err.Error(), "module is disabled") {
			t.Skipf("notification module is disabled on the running RustFS; skipping: %v", err)
		}
		t.Fatalf("could not register the notification target fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := client.ResetNotificationTarget(context.Background(), "notify_webhook", bucketNotificationTargetID); err != nil {
			t.Logf("could not reset notification target fixture %s: %v", bucketNotificationTargetID, err)
		}
	})
}

func TestAccBucketNotificationResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-notif-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_notification.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccBucketNotificationPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccNotificationConfig(name, []string{"s3:ObjectCreated:*", "s3:ObjectRemoved:*"}, "uploads/", ".jpg"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "queue.0.arn", "arn:rustfs:sqs::PRIMARY:webhook"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.filter.prefix", "uploads/"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.filter.suffix", ".jpg"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.events.#", "2"),
					testAccCheckBucketNotificationOnServer(t, name, "uploads/", ".jpg", 2),
				),
			},
			{
				// Update: narrower event set and a different prefix/suffix.
				Config: testAccNotificationConfig(name, []string{"s3:ObjectCreated:*"}, "incoming/", ".png"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "queue.0.filter.prefix", "incoming/"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.filter.suffix", ".png"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.events.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "queue.0.events.*", "s3:ObjectCreated:*"),
					testAccCheckBucketNotificationOnServer(t, name, "incoming/", ".png", 1),
				),
			},
			{
				// Explicit computed consistency: the post-apply plan for the
				// configuration just applied must be empty.
				Config:             testAccNotificationConfig(name, []string{"s3:ObjectCreated:*"}, "incoming/", ".png"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: clear the notification configuration behind
				// Terraform's back; the proposed plan must be non-empty.
				Config:             testAccNotificationConfig(name, []string{"s3:ObjectCreated:*"}, "incoming/", ".png"),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketNotificationOnServer(t, name, "incoming/", ".png", 1),
					testAccMutateBucketNotificationOutOfBand(t, name),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				Config: testAccNotificationConfig(name, []string{"s3:ObjectCreated:*"}, "incoming/", ".png"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketNotificationOnServer(t, name, "incoming/", ".png", 1),
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

func testAccNotificationConfig(bucket string, events []string, prefix, suffix string) string {
	quotedEvents := make([]string, len(events))
	for i, event := range events {
		quotedEvents[i] = fmt.Sprintf("%q", event)
	}
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_notification" "test" {
  bucket = rustfs_bucket.test.name

  queue = [
    {
      arn    = "arn:rustfs:sqs::%s:webhook"
      events = [%s]
      filter = {
        prefix = "%s"
        suffix = "%s"
      }
    }
  ]
}
`, bucket, bucketNotificationTargetID, strings.Join(quotedEvents, ", "), prefix, suffix)
}

func TestAccBucketReplicationResource_basic(t *testing.T) {
	peer, err := remoteTargetPeerConfig(t)
	if err != nil {
		t.Skipf("skipping replication acceptance test: %v", err)
	}
	name := fmt.Sprintf("tf-test-repl-%d", acctest.RandInt())
	srcBucket := name + "-src"
	dstBucket := name + "-dst"
	resourceName := "rustfs_bucket_replication.test"

	// The destination bucket lives on the peer and must be versioned before a
	// remote target can be registered against it; the provider cannot manage it
	// through the primary endpoint.
	peerClient, err := newTestS3Client(peer.s3Endpoint, peer.accessKey, peer.secretKey)
	if err != nil {
		t.Fatalf("creating peer S3 client: %v", err)
	}
	ctx := context.Background()
	if _, err := peerClient.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(dstBucket)}); err != nil {
		t.Fatalf("creating destination bucket on peer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = peerClient.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(dstBucket)})
	})
	if _, err := peerClient.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(dstBucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("enabling versioning on the destination bucket: %v", err)
	}

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketAndRemoteTargetDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccReplicationConfig(name, peer, "Enabled"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrPair(resourceName, "role", "rustfs_remote_target.test", "arn"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.status", "Enabled"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.filter.prefix", "logs/"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.destination.bucket", fmt.Sprintf("arn:aws:s3:::%s", dstBucket)),
					testAccCheckBucketReplicationOnServer(t, srcBucket, "Enabled"),
				),
			},
			{
				// Update: Rule status is mutable in place, so this exercises the
				// Update path instead of recreating the resource.
				Config: testAccReplicationConfig(name, peer, "Disabled"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.status", "Disabled"),
					testAccCheckBucketReplicationOnServer(t, srcBucket, "Disabled"),
				),
			},
			{
				// Explicit computed consistency: the post-apply plan for the
				// configuration just applied must be empty.
				Config:             testAccReplicationConfig(name, peer, "Disabled"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: re-enable the rule on the server behind
				// Terraform's back; the proposed plan must be non-empty.
				Config:             testAccReplicationConfig(name, peer, "Disabled"),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketReplicationOnServer(t, srcBucket, "Disabled"),
					testAccMutateBucketReplicationStatusOutOfBand(t, srcBucket, "Enabled"),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				Config: testAccReplicationConfig(name, peer, "Disabled"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.status", "Disabled"),
					testAccCheckBucketReplicationOnServer(t, srcBucket, "Disabled"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        srcBucket,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

// --- server read-back and out-of-band mutation helpers ----------------------
//
// Every check below reads the value back from the live RustFS endpoint, so an
// acceptance step asserts the server really stored what Terraform applied
// rather than only echoing Terraform's own state.

// testAccMutateBucketTagOutOfBand retags the bucket behind Terraform's back.
func testAccMutateBucketTagOutOfBand(t *testing.T, bucket, value string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		_, err = client.PutBucketTagging(context.Background(), &s3.PutBucketTaggingInput{
			Bucket: aws.String(bucket),
			Tagging: &s3types.Tagging{
				TagSet: []s3types.Tag{
					{Key: aws.String("env"), Value: aws.String(value)},
				},
			},
		})
		if err != nil {
			return fmt.Errorf("out-of-band PutBucketTagging(%s, %s): %w", bucket, value, err)
		}
		return nil
	}
}

// testAccBucketOwnershipControlsAvailable reports whether the running RustFS
// implements PutBucketOwnershipControls. RustFS 1.0.3 answers it with 501
// NotImplemented, which makes the bucket resource's only in-place mutable
// attribute unusable there; the acceptance test adapts instead of failing.
func testAccBucketOwnershipControlsAvailable(t *testing.T) bool {
	t.Helper()
	// The probe talks to a live server before resource.ParallelTest runs, so it
	// has to honour the acceptance precheck itself: without TF_ACC the rest of
	// the suite skips inside ParallelTest, which this call never reaches.
	testAccPreCheck(t)

	client, err := testAccS3Client()
	if err != nil {
		t.Fatalf("creating S3 client: %v", err)
	}
	ctx := context.Background()
	probeBucket := fmt.Sprintf("tf-test-probe-own-%d", acctest.RandInt())
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(probeBucket)}); err != nil {
		t.Fatalf("creating ownership probe bucket: %v", err)
	}
	defer func() {
		if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(probeBucket)}); err != nil {
			t.Logf("could not delete ownership probe bucket %s: %v", probeBucket, err)
		}
	}()

	_, err = client.PutBucketOwnershipControls(ctx, &s3.PutBucketOwnershipControlsInput{
		Bucket: aws.String(probeBucket),
		OwnershipControls: &s3types.OwnershipControls{
			Rules: []s3types.OwnershipControlsRule{
				{ObjectOwnership: s3types.ObjectOwnershipBucketOwnerEnforced},
			},
		},
	})
	if err == nil {
		return true
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NotImplemented" {
		return false
	}
	t.Fatalf("probing PutBucketOwnershipControls: %v", err)
	return false
}

// testAccCheckBucketOwnershipOnServer asserts the ownership controls RustFS returns.
func testAccCheckBucketOwnershipOnServer(t *testing.T, bucket, want string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		out, err := client.GetBucketOwnershipControls(context.Background(), &s3.GetBucketOwnershipControlsInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("GetBucketOwnershipControls(%s): %w", bucket, err)
		}
		if out.OwnershipControls == nil || len(out.OwnershipControls.Rules) == 0 {
			return fmt.Errorf("bucket %s reports no ownership controls", bucket)
		}
		if got := string(out.OwnershipControls.Rules[0].ObjectOwnership); got != want {
			return fmt.Errorf("bucket %s object ownership = %q, want %q", bucket, got, want)
		}
		return nil
	}
}

// testAccCheckBucketTagOnServer asserts a tag the server reports for the bucket.
func testAccCheckBucketTagOnServer(t *testing.T, bucket, key, want string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		out, err := client.GetBucketTagging(context.Background(), &s3.GetBucketTaggingInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("GetBucketTagging(%s): %w", bucket, err)
		}
		for _, tag := range out.TagSet {
			if aws.ToString(tag.Key) == key {
				if got := aws.ToString(tag.Value); got != want {
					return fmt.Errorf("bucket %s tag %q = %q, want %q", bucket, key, got, want)
				}
				return nil
			}
		}
		return fmt.Errorf("bucket %s has no tag %q", bucket, key)
	}
}

// testAccCheckBucketVersioningOnServer asserts the versioning status RustFS reports.
func testAccCheckBucketVersioningOnServer(t *testing.T, bucket, want string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		out, err := client.GetBucketVersioning(context.Background(), &s3.GetBucketVersioningInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("GetBucketVersioning(%s): %w", bucket, err)
		}
		if got := string(out.Status); got != want {
			return fmt.Errorf("bucket %s versioning status = %q, want %q", bucket, got, want)
		}
		return nil
	}
}

// testAccMutateBucketVersioningOutOfBand changes versioning behind Terraform's back.
func testAccMutateBucketVersioningOutOfBand(t *testing.T, bucket, status string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		_, err = client.PutBucketVersioning(context.Background(), &s3.PutBucketVersioningInput{
			Bucket: aws.String(bucket),
			VersioningConfiguration: &s3types.VersioningConfiguration{
				Status: s3types.BucketVersioningStatus(status),
			},
		})
		if err != nil {
			return fmt.Errorf("out-of-band PutBucketVersioning(%s, %s): %w", bucket, status, err)
		}
		return nil
	}
}

// testAccCheckBucketEncryptionOnServer asserts the default SSE rule RustFS returns.
func testAccCheckBucketEncryptionOnServer(t *testing.T, bucket, algorithm, kmsKey string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		out, err := client.GetBucketEncryption(context.Background(), &s3.GetBucketEncryptionInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("GetBucketEncryption(%s): %w", bucket, err)
		}
		cfg := out.ServerSideEncryptionConfiguration
		if cfg == nil || len(cfg.Rules) == 0 {
			return fmt.Errorf("bucket %s reports no encryption rules", bucket)
		}
		apply := cfg.Rules[0].ApplyServerSideEncryptionByDefault
		if apply == nil {
			return fmt.Errorf("bucket %s reports no default encryption", bucket)
		}
		if got := string(apply.SSEAlgorithm); got != algorithm {
			return fmt.Errorf("bucket %s SSE algorithm = %q, want %q", bucket, got, algorithm)
		}
		if got := aws.ToString(apply.KMSMasterKeyID); got != kmsKey {
			return fmt.Errorf("bucket %s KMS master key = %q, want %q", bucket, got, kmsKey)
		}
		return nil
	}
}

// testAccMutateBucketEncryptionOutOfBand changes the SSE default behind Terraform's back.
func testAccMutateBucketEncryptionOutOfBand(t *testing.T, bucket, algorithm, kmsKey string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		apply := &s3types.ServerSideEncryptionByDefault{
			SSEAlgorithm: s3types.ServerSideEncryption(algorithm),
		}
		if kmsKey != "" {
			apply.KMSMasterKeyID = aws.String(kmsKey)
		}
		_, err = client.PutBucketEncryption(context.Background(), &s3.PutBucketEncryptionInput{
			Bucket: aws.String(bucket),
			ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
				Rules: []s3types.ServerSideEncryptionRule{
					{ApplyServerSideEncryptionByDefault: apply},
				},
			},
		})
		if err != nil {
			return fmt.Errorf("out-of-band PutBucketEncryption(%s, %s): %w", bucket, algorithm, err)
		}
		return nil
	}
}

// testAccCheckBucketObjectLockOnServer asserts the Object Lock default retention
// RustFS reports.
func testAccCheckBucketObjectLockOnServer(t *testing.T, bucket, mode string, days int) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		out, err := client.GetObjectLockConfiguration(context.Background(), &s3.GetObjectLockConfigurationInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("GetObjectLockConfiguration(%s): %w", bucket, err)
		}
		cfg := out.ObjectLockConfiguration
		if cfg == nil || cfg.Rule == nil || cfg.Rule.DefaultRetention == nil {
			return fmt.Errorf("bucket %s reports no default object lock retention", bucket)
		}
		retention := cfg.Rule.DefaultRetention
		if got := string(retention.Mode); got != mode {
			return fmt.Errorf("bucket %s default retention mode = %q, want %q", bucket, got, mode)
		}
		if retention.Days == nil {
			return fmt.Errorf("bucket %s default retention has no days", bucket)
		}
		if got := int(*retention.Days); got != days {
			return fmt.Errorf("bucket %s default retention days = %d, want %d", bucket, got, days)
		}
		return nil
	}
}

// testAccMutateBucketObjectLockOutOfBand changes the default retention behind
// Terraform's back.
func testAccMutateBucketObjectLockOutOfBand(t *testing.T, bucket string, days int) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		_, err = client.PutObjectLockConfiguration(context.Background(), &s3.PutObjectLockConfigurationInput{
			Bucket: aws.String(bucket),
			ObjectLockConfiguration: &s3types.ObjectLockConfiguration{
				ObjectLockEnabled: s3types.ObjectLockEnabledEnabled,
				Rule: &s3types.ObjectLockRule{
					DefaultRetention: &s3types.DefaultRetention{
						Mode: s3types.ObjectLockRetentionModeGovernance,
						Days: aws.Int32(int32(days)), // #nosec G115 - small positive test value
					},
				},
			},
		})
		if err != nil {
			return fmt.Errorf("out-of-band PutObjectLockConfiguration(%s, %d): %w", bucket, days, err)
		}
		return nil
	}
}

// testAccCheckBucketNotificationOnServer asserts the single queue notification
// configuration RustFS returns.
func testAccCheckBucketNotificationOnServer(t *testing.T, bucket, prefix, suffix string, events int) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		out, err := client.GetBucketNotificationConfiguration(context.Background(), &s3.GetBucketNotificationConfigurationInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("GetBucketNotificationConfiguration(%s): %w", bucket, err)
		}
		if len(out.QueueConfigurations) != 1 {
			return fmt.Errorf("bucket %s reports %d queue notifications, want 1", bucket, len(out.QueueConfigurations))
		}
		cfg := out.QueueConfigurations[0]
		if got := len(cfg.Events); got != events {
			return fmt.Errorf("bucket %s queue notification events = %d, want %d", bucket, got, events)
		}
		var gotPrefix, gotSuffix string
		if cfg.Filter != nil && cfg.Filter.Key != nil {
			for _, rule := range cfg.Filter.Key.FilterRules {
				switch rule.Name {
				case s3types.FilterRuleNamePrefix:
					gotPrefix = aws.ToString(rule.Value)
				case s3types.FilterRuleNameSuffix:
					gotSuffix = aws.ToString(rule.Value)
				}
			}
		}
		if gotPrefix != prefix || gotSuffix != suffix {
			return fmt.Errorf("bucket %s queue notification filter = (%q, %q), want (%q, %q)", bucket, gotPrefix, gotSuffix, prefix, suffix)
		}
		return nil
	}
}

// testAccMutateBucketNotificationOutOfBand clears the notification
// configuration behind Terraform's back.
func testAccMutateBucketNotificationOutOfBand(t *testing.T, bucket string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		_, err = client.PutBucketNotificationConfiguration(context.Background(), &s3.PutBucketNotificationConfigurationInput{
			Bucket:                    aws.String(bucket),
			NotificationConfiguration: &s3types.NotificationConfiguration{},
		})
		if err != nil {
			return fmt.Errorf("out-of-band PutBucketNotificationConfiguration(%s): %w", bucket, err)
		}
		return nil
	}
}

// testAccCheckBucketReplicationOnServer asserts the status of the first
// replication rule RustFS returns for the bucket.
func testAccCheckBucketReplicationOnServer(t *testing.T, bucket, want string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		out, err := client.GetBucketReplication(context.Background(), &s3.GetBucketReplicationInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("GetBucketReplication(%s): %w", bucket, err)
		}
		cfg := out.ReplicationConfiguration
		if cfg == nil || len(cfg.Rules) == 0 {
			return fmt.Errorf("bucket %s reports no replication rules", bucket)
		}
		if got := string(cfg.Rules[0].Status); got != want {
			return fmt.Errorf("bucket %s replication rule status = %q, want %q", bucket, got, want)
		}
		return nil
	}
}

// testAccMutateBucketReplicationStatusOutOfBand flips the status of the existing
// replication rule behind Terraform's back, preserving role and destination.
func testAccMutateBucketReplicationStatusOutOfBand(t *testing.T, bucket, status string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		ctx := context.Background()
		out, err := client.GetBucketReplication(ctx, &s3.GetBucketReplicationInput{Bucket: aws.String(bucket)})
		if err != nil {
			return fmt.Errorf("GetBucketReplication(%s): %w", bucket, err)
		}
		cfg := out.ReplicationConfiguration
		if cfg == nil || len(cfg.Rules) == 0 {
			return fmt.Errorf("bucket %s reports no replication rules to mutate", bucket)
		}
		cfg.Rules[0].Status = s3types.ReplicationRuleStatus(status)
		if _, err := client.PutBucketReplication(ctx, &s3.PutBucketReplicationInput{
			Bucket:                   aws.String(bucket),
			ReplicationConfiguration: cfg,
		}); err != nil {
			return fmt.Errorf("out-of-band PutBucketReplication(%s, %s): %w", bucket, status, err)
		}
		return nil
	}
}

func testAccReplicationConfig(bucket string, peer *remoteTargetPeer, status string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "src" {
  name = "%s-src"
}

resource "rustfs_bucket_versioning" "src" {
  bucket = rustfs_bucket.src.name
  status = "Enabled"
}

resource "rustfs_remote_target" "test" {
  type          = "replication"
  endpoint      = "%s"
  access_key    = "%s"
  secret_key    = "%s"
  secure        = false
  bucket        = rustfs_bucket.src.name
  target_bucket = "%s-dst"

  depends_on = [rustfs_bucket_versioning.src]
}

resource "rustfs_bucket_replication" "test" {
  bucket = rustfs_bucket.src.name
  role   = rustfs_remote_target.test.arn

  depends_on = [rustfs_remote_target.test]

  rule = [
    {
      status = "%s"
      filter = {
        prefix = "logs/"
      }
      destination = {
        bucket = "arn:aws:s3:::%s-dst"
      }
    }
  ]
}
`, bucket, peer.endpoint, peer.accessKey, peer.secretKey, bucket, status, bucket)
}
