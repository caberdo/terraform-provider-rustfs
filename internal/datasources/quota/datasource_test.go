package quota

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestQuotaDataSourceSchema(t *testing.T) {
	d := NewQuotaDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.TODO(), datasource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, attr := range []string{"bucket", "quota", "quota_type"} {
		if _, ok := attrs[attr]; !ok {
			t.Errorf("expected %s attribute", attr)
		}
	}
}

func TestQuotaDataSourceMetadata(t *testing.T) {
	d := NewQuotaDataSource()
	resp := &datasource.MetadataResponse{}
	d.Metadata(context.TODO(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_quota" {
		t.Errorf("expected rustfs_quota, got %s", resp.TypeName)
	}
}
