package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestKmsConfigDataSourceSchema(t *testing.T) {
	d := NewKmsConfigDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(nil, datasource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"backend", "cache_enabled", "cache_max_keys", "cache_ttl_seconds", "default_key_id",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}
}

func TestKmsConfigDataSourceMetadata(t *testing.T) {
	d := NewKmsConfigDataSource()
	resp := &datasource.MetadataResponse{}
	d.Metadata(nil, datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_kms_config" {
		t.Errorf("expected rustfs_kms_config, got %s", resp.TypeName)
	}
}

func TestAccKmsConfigDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_kms_config" "current" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_kms_config.current", "backend"),
				),
			},
		},
	})
}
