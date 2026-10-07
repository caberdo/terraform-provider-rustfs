package users

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
	"github.com/weinmann-emt/terraform-provider-rustfs/internal/models"
)

var _ datasource.DataSource = &UsersDataSource{}

type UsersDataSource struct {
	client *client.AllClient
}

func NewUsersDataSource() datasource.DataSource {
	return &UsersDataSource{}
}

func (d *UsersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *UsersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "List RustFS IAM users",
		MarkdownDescription: "List all RustFS IAM users, optionally filtered by bucket name",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Optional:    true,
				Description: "Filter users by bucket name.",
			},
			"access_keys": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "List of user access keys.",
			},
		},
	}
}

func (d *UsersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *UsersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config models.UsersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	users, err := d.client.RustClient.ListUsers(config.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing users",
			"Could not list users: "+err.Error(),
		)
		return
	}

	var keys []string
	for _, u := range users {
		keys = append(keys, u.AccessKey)
	}

	accessKeys, diags := types.ListValueFrom(ctx, types.StringType, keys)
	resp.Diagnostics.Append(diags...)

	config.AccessKeys = accessKeys
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
