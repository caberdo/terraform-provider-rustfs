package iam_policies

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
	"github.com/weinmann-emt/terraform-provider-rustfs/internal/models"
)

var _ datasource.DataSource = &IAMPoliciesDataSource{}

type IAMPoliciesDataSource struct {
	client *client.AllClient
}

func NewIAMPoliciesDataSource() datasource.DataSource {
	return &IAMPoliciesDataSource{}
}

func (d *IAMPoliciesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_iam_policies"
}

func (d *IAMPoliciesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "List all canned IAM policies",
		MarkdownDescription: "List all canned IAM policies available on the RustFS cluster",
		Attributes: map[string]schema.Attribute{
			"policies": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Name of the canned policy.",
						},
					},
				},
				Description: "List of canned policy names.",
			},
		},
	}
}

func (d *IAMPoliciesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *client.AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = c
}

func (d *IAMPoliciesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config models.IAMPoliciesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	names, err := d.client.RustClient.ListCannedPolicies()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing canned policies",
			"Could not list canned policies: "+err.Error(),
		)
		return
	}

	summaries := make([]models.IamPolicySummaryModel, 0, len(names))
	for _, name := range names {
		summaries = append(summaries, models.IamPolicySummaryModel{Name: types.StringValue(name)})
	}
	policies, diags := types.ListValueFrom(ctx, types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"name": types.StringType,
		},
	}, summaries)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	config.Policies = policies
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
