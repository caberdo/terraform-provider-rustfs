package iam_backup

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

var _ datasource.DataSource = &IamBackupDataSource{}

type IamBackupDataSource struct {
	client *client.AllClient
}

func NewIamBackupDataSource() datasource.DataSource {
	return &IamBackupDataSource{}
}

func (d *IamBackupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_iam_backup"
}

func (d *IamBackupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Export RustFS IAM data as a ZIP archive",
		MarkdownDescription: "Export all RustFS IAM entities (users, groups, policies, service accounts) as a base64-encoded ZIP archive",
		Attributes: map[string]schema.Attribute{
			"content_base64": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "Base64-encoded ZIP archive containing all IAM data.",
			},
		},
	}
}

func (d *IamBackupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *client.AllClient, got: %T.", req.ProviderData),
		)
		return
	}
	d.client = c
}

func (d *IamBackupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config models.IamBackupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data, err := d.client.RustClient.ExportIam()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error exporting IAM data",
			"Could not export IAM data: "+err.Error(),
		)
		return
	}

	config.ContentBase64 = types.StringValue(base64.StdEncoding.EncodeToString(data))
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
