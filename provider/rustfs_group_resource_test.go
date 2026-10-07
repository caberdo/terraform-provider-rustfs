package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestGroupResourceSchema(t *testing.T) {
	r := NewGroupResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	if _, ok := attrs["name"]; !ok {
		t.Error("expected name attribute")
	}
	if _, ok := attrs["status"]; !ok {
		t.Error("expected status attribute")
	}
	if _, ok := attrs["members"]; !ok {
		t.Error("expected members attribute")
	}
}

func TestGroupResourceMetadata(t *testing.T) {
	r := NewGroupResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_group" {
		t.Errorf("expected rustfs_group, got %s", resp.TypeName)
	}
}
