package bucket_metadata_backup

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
	"github.com/weinmann-emt/terraform-provider-rustfs/internal/models"
)

var _ datasource.DataSource = &BucketMetadataBackupDataSource{}

type BucketMetadataBackupDataSource struct {
	client *client.AllClient
}

func NewBucketMetadataBackupDataSource() datasource.DataSource {
	return &BucketMetadataBackupDataSource{}
}

func (d *BucketMetadataBackupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_metadata_backup"
}

func (d *BucketMetadataBackupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Export bucket metadata as a ZIP archive",
		Attributes: map[string]schema.Attribute{
			"content_base64": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "Base64-encoded ZIP archive with bucket metadata.",
			},
		},
	}
}

func (d *BucketMetadataBackupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.AllClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *client.AllClient, got: %T.", req.ProviderData))
		return
	}
	d.client = c
}

func (d *BucketMetadataBackupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config models.BucketMetadataBackupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data, err := d.client.RustClient.ExportBucketMetadata()
	if err != nil {
		resp.Diagnostics.AddError("Error exporting bucket metadata", "Could not export: "+err.Error())
		return
	}

	config.ContentBase64 = types.StringValue(base64.StdEncoding.EncodeToString(data))
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
