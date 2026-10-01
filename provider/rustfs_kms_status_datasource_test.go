package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestKmsStatusDataSourceSchema(t *testing.T) {
	d := NewKmsStatusDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(nil, datasource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"backend_type", "backend_status", "cache_enabled", "cache_stats",
		"default_key_id", "capabilities", "cluster_config",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}
}

func TestKmsStatusDataSourceMetadata(t *testing.T) {
	d := NewKmsStatusDataSource()
	resp := &datasource.MetadataResponse{}
	d.Metadata(nil, datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_kms_status" {
		t.Errorf("expected rustfs_kms_status, got %s", resp.TypeName)
	}
}
