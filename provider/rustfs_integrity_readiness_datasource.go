// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &IntegrityReadinessDataSource{}

// IntegrityReadinessDataSource reads the deployment's shard-integrity readiness
// switches.
type IntegrityReadinessDataSource struct {
	client *AllClient
}

type IntegrityReadinessDataSourceModel struct {
	ID                      types.String `tfsdk:"id"`
	LocalReaderSupported    types.Bool   `tfsdk:"local_reader_supported"`
	WriteRequested          types.Bool   `tfsdk:"write_requested"`
	FleetOperatorAttested   types.Bool   `tfsdk:"fleet_operator_attested"`
	NewWritesEnabledHere    types.Bool   `tfsdk:"new_writes_enabled_here"`
	FleetCapabilityVerified types.Bool   `tfsdk:"fleet_capability_verified"`
	UnverifiedRequirements  types.List   `tfsdk:"unverified_requirements"`
}

func NewIntegrityReadinessDataSource() datasource.DataSource {
	return &IntegrityReadinessDataSource{}
}

func (d *IntegrityReadinessDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_integrity_readiness"
}

func (d *IntegrityReadinessDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Shard-integrity readiness reported by GET /rustfs/admin/v3/integrity/readiness: whether " +
		"protected writes are requested and activated on this node, whether the fleet operator attested to the " +
		"rollout, and which requirements RustFS still considers unverified. A `migrate` integrity job only runs " +
		"when `new_writes_enabled_here` is true, which needs both `RUSTFS_SHARD_INTEGRITY_WRITE` and " +
		"`RUSTFS_SHARD_INTEGRITY_FLEET_CONFIRMED` on every node."
	resp.Schema = schema.Schema{
		Description:         "Read RustFS shard-integrity readiness",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"local_reader_supported": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether this node can read shard-integrity metadata.",
			},
			"write_requested": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether protected writes were requested through `RUSTFS_SHARD_INTEGRITY_WRITE`.",
			},
			"fleet_operator_attested": schema.BoolAttribute{
				Computed: true,
				Description: "Whether the operator attested to the fleet rollout through " +
					"`RUSTFS_SHARD_INTEGRITY_FLEET_CONFIRMED`.",
			},
			"new_writes_enabled_here": schema.BoolAttribute{
				Computed: true,
				Description: "Whether new writes get independent integrity commitments on this node. `migrate` " +
					"integrity jobs need this to be true.",
			},
			"fleet_capability_verified": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether RustFS verified that the whole fleet can read and write the new format.",
			},
			"unverified_requirements": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Requirements RustFS still reports as unverified.",
			},
		},
	}
}

func (d *IntegrityReadinessDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *IntegrityReadinessDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config IntegrityReadinessDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	readiness, err := d.client.RustClient.IntegrityReadiness(ctx)
	if err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			resp.Diagnostics.AddError(
				"Data-integrity readiness is not available on this RustFS server",
				err.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_integrity_readiness data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading data-integrity readiness",
			"Could not read GET /rustfs/admin/v3/integrity/readiness: "+err.Error(),
		)
		return
	}

	requirements := make([]string, 0, len(readiness.UnverifiedRequirements))
	requirements = append(requirements, readiness.UnverifiedRequirements...)

	config.ID = types.StringValue("integrity-readiness")
	config.LocalReaderSupported = types.BoolValue(readiness.LocalReaderSupported)
	config.WriteRequested = types.BoolValue(readiness.WriteRequested)
	config.FleetOperatorAttested = types.BoolValue(readiness.FleetOperatorAttested)
	config.NewWritesEnabledHere = types.BoolValue(readiness.NewWritesEnabledHere)
	config.FleetCapabilityVerified = types.BoolValue(readiness.FleetCapabilityVerified)
	config.UnverifiedRequirements = stringListValue(requirements)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func stringListValue(values []string) types.List {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.ListValueMust(types.StringType, elements)
}

func init() {
	registerDataSource(NewIntegrityReadinessDataSource)
}
