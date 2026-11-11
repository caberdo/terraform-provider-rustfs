// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &PoolsDataSource{}

type PoolsDataSource struct {
	client *AllClient
}

type PoolsDataSourceModel struct {
	Names types.List `tfsdk:"names"`
	Pools types.List `tfsdk:"pools"`
}

// poolDiagnosticsModel is one entry of the nested pools list. The diagnostics
// are null when the server predates RustFS 1.0.5, which does not send them.
type poolDiagnosticsModel struct {
	Name                    types.String `tfsdk:"name"`
	Status                  types.String `tfsdk:"status"`
	Availability            types.String `tfsdk:"availability"`
	CapacityStatus          types.String `tfsdk:"capacity_status"`
	PoolMetadataStatus      types.String `tfsdk:"pool_metadata_status"`
	RebalanceMetadataStatus types.String `tfsdk:"rebalance_metadata_status"`
	ObservedAt              types.String `tfsdk:"observed_at"`
}

var poolDiagnosticsAttrTypes = map[string]attr.Type{
	"name":                      types.StringType,
	"status":                    types.StringType,
	"availability":              types.StringType,
	"capacity_status":           types.StringType,
	"pool_metadata_status":      types.StringType,
	"rebalance_metadata_status": types.StringType,
	"observed_at":               types.StringType,
}

func NewPoolsDataSource() datasource.DataSource {
	return &PoolsDataSource{}
}

func (d *PoolsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pools"
}

func (d *PoolsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List RustFS storage pools",
		MarkdownDescription: "List all RustFS storage pools, with availability, capacity and " +
			"metadata-freshness diagnostics on servers that report them.",
		Attributes: map[string]schema.Attribute{
			"names": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "List of storage pool names.",
			},
			"pools": schema.ListNestedAttribute{
				Computed: true,
				Description: "One entry per storage pool, in the order the server reports them. " +
					"The diagnostic attributes are null when the server does not send them (RustFS before 1.0.5).",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Storage pool name: the command line the pool was started with.",
						},
						"status": schema.StringAttribute{
							Computed:    true,
							Description: "Pool status the server reports.",
						},
						"availability": schema.StringAttribute{
							Computed: true,
							Description: "Pool availability: `online`, `offline` or `unknown`. Null on servers " +
								"before RustFS 1.0.5.",
						},
						"capacity_status": schema.StringAttribute{
							Computed: true,
							Description: "Capacity state: `live`, `partial` or `unknown`. Null on servers before " +
								"RustFS 1.0.5.",
						},
						"pool_metadata_status": schema.StringAttribute{
							Computed:    true,
							Description: "Metadata freshness of the pool. Null on servers before RustFS 1.0.5.",
						},
						"rebalance_metadata_status": schema.StringAttribute{
							Computed:    true,
							Description: "Metadata freshness of the rebalance state. Null on servers before RustFS 1.0.5.",
						},
						"observed_at": schema.StringAttribute{
							Computed: true,
							Description: "When the server sampled these diagnostics (RFC3339). Null on servers " +
								"before RustFS 1.0.5.",
						},
					},
				},
			},
		},
	}
}

func (d *PoolsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *PoolsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PoolsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pools, err := d.client.RustClient.ListPools(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing pools",
			"Could not list pools: "+err.Error(),
		)
		return
	}

	var names []string
	diagnostics := make([]poolDiagnosticsModel, 0, len(pools))
	for _, p := range pools {
		names = append(names, p.Name)
		diagnostics = append(diagnostics, poolDiagnosticsModel{
			Name:                    types.StringValue(p.Name),
			Status:                  types.StringValue(p.Status),
			Availability:            nullableStringPointer(p.Availability),
			CapacityStatus:          nullableStringPointer(p.CapacityStatus),
			PoolMetadataStatus:      nullableStringPointer(p.PoolMetadataStatus),
			RebalanceMetadataStatus: nullableStringPointer(p.RebalanceMetadataStatus),
			ObservedAt:              nullableStringPointer(p.ObservedAt),
		})
	}

	poolNames, diags := types.ListValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(diags...)
	poolList, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: poolDiagnosticsAttrTypes}, diagnostics)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Names = poolNames
	config.Pools = poolList
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func init() {
	registerDataSource(NewPoolsDataSource)
}
