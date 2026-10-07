package user_mfa

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestUserMfaDataSourceSchema(t *testing.T) {
	d := NewUserMfaDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.TODO(), datasource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, want := range []string{"access_key", "enabled", "activated_at", "recovery_codes_remaining"} {
		if _, ok := attrs[want]; !ok {
			t.Errorf("expected %s attribute", want)
		}
	}
}

func TestUserMfaDataSourceMetadata(t *testing.T) {
	d := NewUserMfaDataSource()
	resp := &datasource.MetadataResponse{}
	d.Metadata(context.TODO(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_user_mfa" {
		t.Errorf("expected rustfs_user_mfa, got %s", resp.TypeName)
	}
}
