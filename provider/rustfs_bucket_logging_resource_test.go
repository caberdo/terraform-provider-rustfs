// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const loggingConfigurationPayload = `<?xml version="1.0" encoding="UTF-8"?>
<BucketLoggingStatus xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<LoggingEnabled>
<TargetBucket>log-target</TargetBucket>
<TargetPrefix>logs/</TargetPrefix>
</LoggingEnabled>
</BucketLoggingStatus>`

const loggingConfigurationEmptyPayload = `<?xml version="1.0" encoding="UTF-8"?>
<BucketLoggingStatus xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></BucketLoggingStatus>`

func TestBuildLoggingStatus(t *testing.T) {
	status := buildLoggingStatus(bucketLoggingModel{
		Bucket:       types.StringValue("tf-test"),
		TargetBucket: types.StringValue("log-target"),
		TargetPrefix: types.StringValue("logs/"),
		TargetGrant: []targetGrantModel{{
			Permission: types.StringValue("FULL_CONTROL"),
			Grantee: &targetGrantGranteeModel{
				Type: types.StringValue("Group"),
				Uri:  types.StringValue("http://acs.amazonaws.com/groups/s3/LogDelivery"),
			},
		}},
	})

	if status.LoggingEnabled == nil {
		t.Fatal("expected logging to be enabled")
	}
	if aws.ToString(status.LoggingEnabled.TargetBucket) != "log-target" {
		t.Errorf("target bucket = %q", aws.ToString(status.LoggingEnabled.TargetBucket))
	}
	if aws.ToString(status.LoggingEnabled.TargetPrefix) != "logs/" {
		t.Errorf("target prefix = %q", aws.ToString(status.LoggingEnabled.TargetPrefix))
	}
	if len(status.LoggingEnabled.TargetGrants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(status.LoggingEnabled.TargetGrants))
	}
	grant := status.LoggingEnabled.TargetGrants[0]
	if grant.Grantee == nil || grant.Grantee.Type != s3types.TypeGroup {
		t.Errorf("grantee = %+v", grant.Grantee)
	}
	if aws.ToString(grant.Grantee.URI) != "http://acs.amazonaws.com/groups/s3/LogDelivery" {
		t.Errorf("grantee uri = %q", aws.ToString(grant.Grantee.URI))
	}
	if grant.Permission != s3types.BucketLogsPermissionFullControl {
		t.Errorf("permission = %q", grant.Permission)
	}
}

func TestFlattenTargetGrants(t *testing.T) {
	grants := flattenTargetGrants([]s3types.TargetGrant{{
		Grantee: &s3types.Grantee{
			Type: s3types.TypeGroup,
			URI:  aws.String("http://acs.amazonaws.com/groups/s3/LogDelivery"),
		},
		Permission: s3types.BucketLogsPermissionFullControl,
	}})

	if len(grants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(grants))
	}
	if grants[0].Grantee == nil {
		t.Fatal("grantee was not flattened")
	}
	if grants[0].Grantee.Type.ValueString() != "Group" {
		t.Errorf("grantee type = %q", grants[0].Grantee.Type.ValueString())
	}
	if grants[0].Grantee.Uri.ValueString() != "http://acs.amazonaws.com/groups/s3/LogDelivery" {
		t.Errorf("grantee uri = %q", grants[0].Grantee.Uri.ValueString())
	}
	if !grants[0].Grantee.Id.IsNull() || !grants[0].Grantee.EmailAddress.IsNull() {
		t.Error("unset grantee members must stay null, not become empty strings")
	}
	if grants[0].Permission.ValueString() != "FULL_CONTROL" {
		t.Errorf("permission = %q", grants[0].Permission.ValueString())
	}
}

func TestBucketLoggingResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewBucketLoggingResource())
	for _, want := range []string{"bucket", "id", "target_bucket", "target_prefix"} {
		if _, ok := sch.GetAttributes()[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
	if _, ok := sch.GetBlocks()["target_grant"]; !ok {
		t.Error("missing target_grant block")
	}
	for name, attr := range sch.GetAttributes() {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestBucketLoggingResourceMetadata(t *testing.T) {
	resp := &resource.MetadataResponse{}
	NewBucketLoggingResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_logging" {
		t.Errorf("expected rustfs_bucket_logging, got %s", resp.TypeName)
	}
}

func TestBucketLoggingResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	loggingPlan := func() *bucketLoggingModel {
		return &bucketLoggingModel{
			Bucket:       types.StringValue("tf-test"),
			TargetBucket: types.StringValue("log-target"),
			TargetPrefix: types.StringValue("logs/"),
		}
	}

	t.Run("create checks the target bucket then enables logging", func(t *testing.T) {
		client, recorder := routedClient(t,
			requestRoute{method: http.MethodHead, status: http.StatusOK},
			requestRoute{method: http.MethodPut, status: http.StatusOK},
		)
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)

		resp := h.create(loggingPlan())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "tf-test")
		recorder.assertRequest(t, "HEAD")
		recorder.assertRequest(t, "PUT")
	})

	t.Run("create refuses a target bucket that does not exist", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{
			method: http.MethodHead,
			status: http.StatusNotFound,
			body:   s3ErrorXML("NoSuchBucket", "gone"),
		})
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)

		resp := h.create(loggingPlan())

		assertErrorDiagnostic(t, resp.Diagnostics, "Logging target bucket does not exist", `Bucket "log-target" does not exist`)
		recorder.assertNoRequest(t, "PUT")
	})

	t.Run("create reports a failed target check", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodHead,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "cannot look"),
		})
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)

		resp := h.create(loggingPlan())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error checking logging target bucket", `Could not check bucket "log-target"`)
	})

	t.Run("create failure reports the server error and writes no state", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodHead, status: http.StatusOK},
			requestRoute{method: http.MethodPut, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "no logging")},
		)
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)
		plan := loggingPlan()

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating bucket logging configuration", "no logging")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("read mirrors the target and prefix", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodGet, status: http.StatusOK, body: loggingConfigurationPayload})
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)

		resp := h.read(&bucketLoggingModel{
			Bucket:       types.StringValue("tf-test"),
			TargetBucket: types.StringValue("other"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("target_bucket"), "log-target")
		assertStateString(t, resp.State, path.Root("target_prefix"), "logs/")
	})

	t.Run("read removes a bucket with logging disabled", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodGet, status: http.StatusOK, body: loggingConfigurationEmptyPayload})
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)

		resp := h.read(&bucketLoggingModel{Bucket: types.StringValue("tf-test")})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("read removes a vanished bucket", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet,
			status: http.StatusNotFound,
			body:   s3ErrorXML("NoSuchBucket", "gone"),
		})
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)

		resp := h.read(&bucketLoggingModel{Bucket: types.StringValue("tf-test")})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("update revalidates the target and writes", func(t *testing.T) {
		client, recorder := routedClient(t,
			requestRoute{method: http.MethodHead, status: http.StatusOK},
			requestRoute{method: http.MethodPut, status: http.StatusOK},
		)
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)

		resp := h.update(loggingPlan(), loggingPlan())

		requireNoDiagnostics(t, resp.Diagnostics)
		recorder.assertRequest(t, "PUT")
	})

	t.Run("update failure is reported", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodHead, status: http.StatusOK},
			requestRoute{method: http.MethodPut, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "no update")},
		)
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)

		resp := h.update(loggingPlan(), loggingPlan())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket logging configuration", "no update")
	})

	t.Run("delete reports a server error", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no disable"),
		})
		h := newCRUDHarness(t, NewBucketLoggingResource(), client)

		resp := h.delete(loggingPlan())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error deleting bucket logging configuration", "no disable")
	})

	t.Run("delete disables logging and tolerates a vanished bucket", func(t *testing.T) {
		for name, status := range map[string]int{
			"server accepts the disable": http.StatusOK,
			"bucket already gone":        http.StatusNotFound,
		} {
			t.Run(name, func(t *testing.T) {
				client, _ := routedClient(t, requestRoute{
					method: http.MethodPut,
					status: status,
					body:   s3ErrorXML("NoSuchBucket", "gone"),
				})
				h := newCRUDHarness(t, NewBucketLoggingResource(), client)

				resp := h.delete(loggingPlan())

				requireNoDiagnostics(t, resp.Diagnostics)
			})
		}
	})
}

func TestAccBucketLogging_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-logging-%d", acctest.RandInt())
	target := fmt.Sprintf("tf-test-logging-target-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_logging.test"

	tfresource.ParallelTest(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []tfresource.TestStep{
			{
				Config: testAccBucketLoggingConfig(name, target, "logs/"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "bucket", name),
					tfresource.TestCheckResourceAttr(resourceName, "target_bucket", target),
					tfresource.TestCheckResourceAttr(resourceName, "target_prefix", "logs/"),
					tfresource.TestCheckResourceAttr(resourceName, "target_grant.#", "1"),
					testAccCheckBucketStillExists(name),
				),
			},
			{
				// In place, with an empty follow-up plan.
				Config: testAccBucketLoggingConfig(name, target, "audit/"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "target_prefix", "audit/"),
					testAccCheckBucketStillExists(name),
				),
			},
			{
				Config:   testAccBucketLoggingConfig(name, target, "audit/"),
				PlanOnly: true,
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

func testAccBucketLoggingConfig(name, target, prefix string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket" "log_target" {
  name = "%s"
}

resource "rustfs_bucket_logging" "test" {
  bucket        = rustfs_bucket.test.name
  target_bucket = rustfs_bucket.log_target.name
  target_prefix = "%s"

  target_grant {
    permission = "FULL_CONTROL"

    grantee {
      type = "Group"
      uri  = "http://acs.amazonaws.com/groups/s3/LogDelivery"
    }
  }

  depends_on = [rustfs_bucket.log_target]
}
`, name, target, prefix)
}

func TestBucketLoggingResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewBucketLoggingResource().(*bucketLoggingResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewBucketLoggingResource().(*bucketLoggingResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Resource Configure Type")
	})
}

func TestBuildLoggingStatusMinimal(t *testing.T) {
	status := buildLoggingStatus(bucketLoggingModel{
		Bucket:       types.StringValue("tf-test"),
		TargetBucket: types.StringValue("log-target"),
		TargetGrant: []targetGrantModel{{
			Permission: types.StringValue("READ"),
		}},
	})

	if status.LoggingEnabled.TargetPrefix != nil {
		t.Errorf("null prefix must not be sent, got %v", status.LoggingEnabled.TargetPrefix)
	}
	if len(status.LoggingEnabled.TargetGrants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(status.LoggingEnabled.TargetGrants))
	}
	if status.LoggingEnabled.TargetGrants[0].Grantee != nil {
		t.Errorf("a grant without a grantee must stay empty, got %+v", status.LoggingEnabled.TargetGrants[0].Grantee)
	}
}

func TestFlattenTargetGrantsWithoutGrantee(t *testing.T) {
	grants := flattenTargetGrants([]s3types.TargetGrant{{Permission: s3types.BucketLogsPermissionRead}})
	if len(grants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(grants))
	}
	if grants[0].Grantee != nil {
		t.Errorf("expected no grantee, got %+v", grants[0].Grantee)
	}
}
