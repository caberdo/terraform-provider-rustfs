package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

var _ datasource.DataSource = &KmsConfigDataSource{}

type KmsConfigDataSource struct {
	client *AllClient
}

type KmsConfigDataSourceModel struct {
	Backend         types.String `tfsdk:"backend"`
	CacheEnabled    types.Bool   `tfsdk:"cache_enabled"`
	CacheMaxKeys    types.Int64  `tfsdk:"cache_max_keys"`
	CacheTTLSeconds types.Int64  `tfsdk:"cache_ttl_seconds"`
	DefaultKeyID    types.String `tfsdk:"default_key_id"`
}

func NewKmsConfigDataSource() datasource.DataSource {
	return &KmsConfigDataSource{}
}

func (d *KmsConfigDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kms_config"
}

func (d *KmsConfigDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Get the RustFS KMS backend configuration",
		MarkdownDescription: "Fetch the current RustFS KMS backend configuration, including the backend type, cache settings, and default key ID.",
		Attributes: map[string]schema.Attribute{
			"backend": schema.StringAttribute{
				Computed:    true,
				Description: "KMS backend type (local, vault-kv2, vault-transit, static, aws).",
			},
			"cache_enabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the KMS key cache is enabled.",
			},
			"cache_max_keys": schema.Int64Attribute{
				Computed:    true,
				Description: "Maximum number of keys held in the cache.",
			},
			"cache_ttl_seconds": schema.Int64Attribute{
				Computed:    true,
				Description: "Cache entry time-to-live in seconds.",
			},
			"default_key_id": schema.StringAttribute{
				Computed:    true,
				Description: "Default KMS key ID. Null when no default key is configured.",
			},
		},
	}
}

func (d *KmsConfigDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *KmsConfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config KmsConfigDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kmsConfig, err := d.client.RustClient.GetKmsConfig()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading KMS config",
			"Could not read KMS config: "+err.Error(),
		)
		return
	}

	config, diags := kmsConfigModelFromConfig(kmsConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func kmsConfigModelFromConfig(kmsConfig *rustfs.KmsConfig) (KmsConfigDataSourceModel, diag.Diagnostics) {
	var config KmsConfigDataSourceModel
	config.Backend = types.StringValue(kmsConfig.Backend)
	config.CacheEnabled = types.BoolValue(kmsConfig.CacheEnabled)
	config.CacheMaxKeys = types.Int64Value(int64(kmsConfig.CacheMaxKeys))
	config.CacheTTLSeconds = types.Int64Value(int64(kmsConfig.CacheTTLSeconds))
	config.DefaultKeyID = stringFromPtr(kmsConfig.DefaultKeyID)
	return config, nil
}
