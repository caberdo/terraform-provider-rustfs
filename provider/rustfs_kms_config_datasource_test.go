package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/weinmann-emt/terraform-provider-rustfs/pkg/rustfs"
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
		a, ok := attrs[name]
		if !ok {
			t.Errorf("expected %s attribute", name)
			continue
		}
		if !a.IsComputed() {
			t.Errorf("expected %s to be computed", name)
		}
		if a.GetDescription() == "" {
			t.Errorf("expected %s to have a description", name)
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

func TestKmsConfigModelFromConfig(t *testing.T) {
	kmsConfig := &rustfs.KmsConfig{
		Backend:         "vault-kv2",
		CacheEnabled:    true,
		CacheMaxKeys:    1000,
		CacheTTLSeconds: 300,
		DefaultKeyID:    stringPtr("key-01"),
	}

	model, diags := kmsConfigModelFromConfig(kmsConfig)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.Backend.ValueString() != "vault-kv2" {
		t.Errorf("expected vault-kv2, got %s", model.Backend.ValueString())
	}
	if !model.CacheEnabled.ValueBool() {
		t.Error("expected cache_enabled true")
	}
	if model.CacheMaxKeys.ValueInt64() != 1000 {
		t.Errorf("expected 1000, got %d", model.CacheMaxKeys.ValueInt64())
	}
	if model.CacheTTLSeconds.ValueInt64() != 300 {
		t.Errorf("expected 300, got %d", model.CacheTTLSeconds.ValueInt64())
	}
	if model.DefaultKeyID.ValueString() != "key-01" {
		t.Errorf("expected key-01, got %s", model.DefaultKeyID.ValueString())
	}
}

func TestKmsConfigModelFromConfigNullDefaultKey(t *testing.T) {
	kmsConfig := &rustfs.KmsConfig{Backend: "local"}

	model, diags := kmsConfigModelFromConfig(kmsConfig)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !model.DefaultKeyID.IsNull() {
		t.Errorf("expected null default_key_id, got %s", model.DefaultKeyID)
	}
}
