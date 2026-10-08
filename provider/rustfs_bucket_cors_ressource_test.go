package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestIsBucketSubresourceAbsent(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		codes []string
		want  bool
	}{
		{
			name: "unrelated 404 substring in hostname is not absent",
			err:  errors.New("cannot reach host api-404.internal"),
			want: false,
		},
		{
			name:  "typed code matches caller supplied code",
			err:   &smithy.GenericAPIError{Code: "NoSuchCORSConfiguration", Message: "not configured"},
			codes: []string{"NoSuchCORSConfiguration"},
			want:  true,
		},
		{
			name: "typed NoSuchBucket is absent",
			err:  &smithy.GenericAPIError{Code: "NoSuchBucket", Message: "gone"},
			want: true,
		},
		{
			name:  "string fallback matches explicit code",
			err:   errors.New("the CORS configuration does not exist: NoSuchCORSConfiguration"),
			codes: []string{"NoSuchCORSConfiguration"},
			want:  true,
		},
		{
			name: "string fallback matches NoSuchBucket",
			err:  errors.New("NoSuchBucket: bucket is gone"),
			want: true,
		},
		{
			name: "nil error is not absent",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBucketSubresourceAbsent(tt.err, tt.codes...); got != tt.want {
				t.Errorf("isBucketSubresourceAbsent(%v, %v) = %v, want %v", tt.err, tt.codes, got, tt.want)
			}
		})
	}
}

func TestFlattenCorsRules(t *testing.T) {
	got := flattenCorsRules([]s3types.CORSRule{
		{
			AllowedMethods: []string{"GET"},
			AllowedOrigins: []string{"https://example.com"},
		},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(got))
	}
	if !got[0].Id.IsNull() {
		t.Errorf("expected null id when the server omits ID, got %q", got[0].Id)
	}
	if !got[0].MaxAgeSeconds.IsNull() {
		t.Errorf("expected null max_age_seconds when the server omits MaxAgeSeconds, got %d", got[0].MaxAgeSeconds)
	}

	populated := flattenCorsRules([]s3types.CORSRule{
		{
			ID:             aws.String("rule-1"),
			MaxAgeSeconds:  aws.Int32(600),
			AllowedMethods: []string{"GET"},
			AllowedOrigins: []string{"*"},
		},
	})
	if populated[0].Id.ValueString() != "rule-1" {
		t.Errorf("expected id rule-1, got %q", populated[0].Id)
	}
	if populated[0].MaxAgeSeconds.ValueInt64() != 600 {
		t.Errorf("expected max_age_seconds 600, got %d", populated[0].MaxAgeSeconds)
	}
}

func TestBucketCorsRessourceSchema(t *testing.T) {
	r := NewBucketCorsRessource()
	resp := &frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), frameworkresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	if _, ok := attrs["bucket"]; !ok {
		t.Error("expected bucket attribute")
	}
	if _, ok := attrs["id"]; !ok {
		t.Error("expected id attribute")
	}

	blocks := resp.Schema.GetBlocks()
	rule, ok := blocks["rule"].(schema.ListNestedBlock)
	if !ok {
		t.Fatalf("expected rule block to be ListNestedBlock, got %T", blocks["rule"])
	}
	for _, want := range []string{"allowed_headers", "allowed_methods", "allowed_origins", "expose_headers", "max_age_seconds", "id"} {
		if _, ok := rule.NestedObject.Attributes[want]; !ok {
			t.Errorf("missing rule attribute %q", want)
		}
	}
}

func TestBucketCorsRessourceMetadata(t *testing.T) {
	r := NewBucketCorsRessource()
	resp := &frameworkresource.MetadataResponse{}
	r.Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_cors" {
		t.Errorf("expected rustfs_bucket_cors, got %s", resp.TypeName)
	}
}

func TestAccBucketCorsResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-cors-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_cors.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketCorsConfig(name, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", name),
					resource.TestCheckResourceAttr(resourceName, "id", name),
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "corsrule1"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.max_age_seconds", "3000"),
				),
			},
			{
				Config: testAccBucketCorsConfig(name, "corsrule2", "https://app.example.com", "DELETE", "GET", "0"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "corsrule2"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.allowed_methods.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.allowed_origins.#", "1"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     name,
			},
		},
	})
}

func testAccBucketCorsConfig(bucket, ruleID, origin, methodOne, methodTwo, maxAge string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_cors" "test" {
  bucket = rustfs_bucket.test.name

  rule {
    id              = "%s"
    allowed_origins = ["%s"]
    allowed_methods = ["%s", "%s"]
    allowed_headers = ["*"]
    expose_headers  = ["ETag"]
    max_age_seconds = %s
  }
}
`, bucket, ruleID, origin, methodOne, methodTwo, maxAge)
}
