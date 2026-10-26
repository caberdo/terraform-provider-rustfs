// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const (
	policyStatusPublicPayload = `<?xml version="1.0" encoding="UTF-8"?>
<PolicyStatus xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsPublic>true</IsPublic></PolicyStatus>`

	policyStatusPrivatePayload = `<?xml version="1.0" encoding="UTF-8"?>
<PolicyStatus xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsPublic>false</IsPublic></PolicyStatus>`
)

func TestBucketPolicyStatusDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewBucketPolicyStatusDataSource())
	for _, want := range []string{"bucket", "id", "is_public"} {
		if _, ok := sch.GetAttributes()[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
	for name, attr := range sch.GetAttributes() {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestBucketPolicyStatusDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewBucketPolicyStatusDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_policy_status" {
		t.Errorf("expected rustfs_bucket_policy_status, got %s", resp.TypeName)
	}
}

func TestBucketPolicyStatusDataSourceRead(t *testing.T) {
	read := func(t *testing.T, status int, body string) *datasource.ReadResponse {
		t.Helper()
		d := NewBucketPolicyStatusDataSource().(*bucketPolicyStatusDataSource)
		d.client = cannedClient(t, status, body)
		sch := dataSourceSchemaOf(t, d)
		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, bucketPolicyStatusModel{Bucket: types.StringValue("tf-test")}),
		}, resp)
		return resp
	}

	t.Run("a public policy reports is_public true", func(t *testing.T) {
		resp := read(t, http.StatusOK, policyStatusPublicPayload)
		requireNoDiagnostics(t, resp.Diagnostics)

		var got bucketPolicyStatusModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
		if !got.IsPublic.ValueBool() {
			t.Error("is_public = false, want true")
		}
		if got.Id.ValueString() != "tf-test" {
			t.Errorf("id = %q", got.Id.ValueString())
		}
	})

	t.Run("a private or absent policy reports is_public false", func(t *testing.T) {
		resp := read(t, http.StatusOK, policyStatusPrivatePayload)
		requireNoDiagnostics(t, resp.Diagnostics)

		var got bucketPolicyStatusModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
		if got.IsPublic.ValueBool() {
			t.Error("is_public = true, want false")
		}
	})

	t.Run("a missing bucket is an error", func(t *testing.T) {
		resp := read(t, http.StatusNotFound, s3ErrorXML("NoSuchBucket", "gone"))
		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading bucket policy status", `Bucket "tf-test" does not exist.`)
	})

	t.Run("a server error is reported", func(t *testing.T) {
		resp := read(t, http.StatusForbidden, s3ErrorXML("AccessDenied", "nope"))
		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading bucket policy status", "nope")
	})
}

func TestAccBucketPolicyStatusDataSource(t *testing.T) {
	name := fmt.Sprintf("tf-test-policy-status-%d", acctest.RandInt())
	resourceName := "data.rustfs_bucket_policy_status.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketPolicyStatusConfig(name, `"arn:aws:iam:::user/rustfsadmin"`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "is_public", "false"),
				),
			},
			{
				// RustFS reports the bucket public only for this exact shape: an
				// explicit wildcard principal granting s3:* on the bucket.
				Config: testAccBucketPolicyStatusConfig(name, `["*"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "is_public", "true"),
				),
			},
		},
	})
}

func testAccBucketPolicyStatusConfig(name, principal string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_policy" "test" {
  bucket = rustfs_bucket.test.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = %s }
      Action    = "s3:*"
      Resource  = "arn:aws:s3:::%s"
    }]
  })
}

data "rustfs_bucket_policy_status" "test" {
  bucket     = rustfs_bucket.test.name
  depends_on = [rustfs_bucket_policy.test]
}
`, name, principal, name)
}

func TestBucketPolicyStatusDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewBucketPolicyStatusDataSource().(*bucketPolicyStatusDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewBucketPolicyStatusDataSource().(*bucketPolicyStatusDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})
}
