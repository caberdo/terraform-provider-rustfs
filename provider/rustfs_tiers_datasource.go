// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &TiersDataSource{}

// TiersDataSource lists the storage tiers configured on the RustFS server
// (GET /rustfs/admin/v3/tier), including each backend's redacted
// configuration.
type TiersDataSource struct {
	client *AllClient
}

type TiersDataSourceModel struct {
	ID    types.String `tfsdk:"id"`
	Tiers types.List   `tfsdk:"tiers"`
}

type tierListModel struct {
	Name       types.String `tfsdk:"name"`
	TierType   types.String `tfsdk:"tier_type"`
	ConfigJSON types.String `tfsdk:"config_json"`
}

func NewTiersDataSource() datasource.DataSource {
	return &TiersDataSource{}
}

func (d *TiersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tiers"
}

func (d *TiersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List the RustFS storage tiers configured for data transition",
		MarkdownDescription: "List the storage tiers `rustfs_tier` manages, with the backend type and the " +
			"backend configuration the server reports for each one.\n\n" +
			"`GET /rustfs/admin/v3/tier` is the only route that returns a tier's backend configuration. " +
			"The server already redacts the credentials it knows about, and the provider redacts them again " +
			"on its side (`secret_key`, `session_token`, Azure `account_key`/`sas_token`, GCS " +
			"`client_secret` and the AWS role tokens) so a server that stops redacting one key cannot leak " +
			"it into Terraform state. Everything else is kept, so `config_json` still describes the backend.\n\n" +
			"This data source is read-only; manage tiers with `rustfs_tier`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"tiers": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Configured tiers sorted by name.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Tier name, which is the identifier `rustfs_tier` manages.",
						},
						"tier_type": schema.StringAttribute{
							Computed:    true,
							Description: "Backend type the server reports, for example `s3`, `azure`, `gcs` or `rustfs`.",
						},
						"config_json": schema.StringAttribute{
							Computed: true,
							Description: "Backend configuration as compact JSON with credential values replaced " +
								"by `REDACTED`. Null when the server returned no backend object for the tier.",
						},
					},
				},
			},
		},
	}
}

func (d *TiersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TiersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config TiersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tiers, err := d.client.RustClient.ListTiers(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing tiers",
			"Could not list RustFS tiers: "+err.Error(),
		)
		return
	}

	// The client sorts already; sorting here as well keeps state deterministic
	// if a future route returns a different order.
	slices.SortFunc(tiers, func(a, b rustfs.TierInfo) int {
		return strings.Compare(a.Name, b.Name)
	})
	models := make([]tierListModel, 0, len(tiers))
	for _, tier := range tiers {
		models = append(models, tierListModel{
			Name:       types.StringValue(tier.Name),
			TierType:   types.StringValue(tier.TierType),
			ConfigJSON: optionalStringValue(tier.Config),
		})
	}

	listed, diags := types.ListValueFrom(ctx, tierListObjectType(), models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	config.ID = types.StringValue("tiers")
	config.Tiers = listed
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func tierListObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"name":        types.StringType,
			"tier_type":   types.StringType,
			"config_json": types.StringType,
		},
	}
}

func init() {
	registerDataSource(NewTiersDataSource)
}
