package bucket_versioning

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestBucketVersioningResourceSchema(t *testing.T) {
	r := NewBucketVersioningResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	if _, ok := attrs["bucket"]; !ok {
		t.Error("expected bucket attribute")
	}
	if _, ok := attrs["status"]; !ok {
		t.Error("expected status attribute")
	}
}

func TestBucketVersioningResourceMetadata(t *testing.T) {
	r := NewBucketVersioningResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_versioning" {
		t.Errorf("expected rustfs_bucket_versioning, got %s", resp.TypeName)
	}
}
