// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ObjectDataCacheStatsDataSource{}

type ObjectDataCacheStatsDataSource struct {
	client *AllClient
}

type ObjectDataCacheStatsDataSourceModel struct {
	ID                   types.String `tfsdk:"id"`
	JSON                 types.String `tfsdk:"json"`
	Mode                 types.String `tfsdk:"mode"`
	Disabled             types.Bool   `tfsdk:"disabled"`
	Entries              types.Int64  `tfsdk:"entries"`
	Lookups              types.Int64  `tfsdk:"lookups"`
	Hits                 types.Int64  `tfsdk:"hits"`
	Fills                types.Int64  `tfsdk:"fills"`
	Invalidations        types.Int64  `tfsdk:"invalidations"`
	InflightFills        types.Int64  `tfsdk:"inflight_fills"`
	SingleflightJoins    types.Int64  `tfsdk:"singleflight_joins"`
	MemoryPressureEvents types.Int64  `tfsdk:"memory_pressure_events"`
}

func NewObjectDataCacheStatsDataSource() datasource.DataSource {
	return &ObjectDataCacheStatsDataSource{}
}

func (d *ObjectDataCacheStatsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_data_cache_stats"
}

func (d *ObjectDataCacheStatsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Statistics of the RustFS object data cache (GET /rustfs/admin/v3/object-data-cache/stats). " +
		"A deployment without a cache reports mode `disabled` and zero counters. This data source only " +
		"observes the cache; its tuning stays in the server configuration."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON response as returned by the RustFS admin API.",
			},
			"mode": schema.StringAttribute{
				Computed:    true,
				Description: "Cache mode reported by the server, or `disabled` when no cache is initialized.",
			},
			"disabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the cache is disabled.",
			},
			"entries": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of cached entries.",
			},
			"lookups": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of cache lookups since startup.",
			},
			"hits": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of cache hits since startup.",
			},
			"fills": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of cache fills since startup.",
			},
			"invalidations": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of cache invalidations since startup.",
			},
			"inflight_fills": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of fills currently in flight.",
			},
			"singleflight_joins": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of lookups that joined an in-flight fill instead of starting one.",
			},
			"memory_pressure_events": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of times memory pressure evicted entries.",
			},
		},
	}
}

func (d *ObjectDataCacheStatsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ObjectDataCacheStatsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	stats, err := d.client.RustClient.GetObjectDataCacheStats(ctx)
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"Object data cache statistics are not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_object_data_cache_stats data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading object data cache statistics",
			"Could not read object data cache statistics, unexpected error: "+err.Error(),
		)
		return
	}

	state := ObjectDataCacheStatsDataSourceModel{
		ID:                   types.StringValue("object-data-cache-stats"),
		JSON:                 types.StringValue(stats.RawJSON),
		Mode:                 types.StringValue(stats.Mode),
		Disabled:             types.BoolValue(stats.Disabled),
		Entries:              types.Int64Value(int64(stats.Entries)),
		Lookups:              types.Int64Value(int64(stats.Lookups)),
		Hits:                 types.Int64Value(int64(stats.Hits)),
		Fills:                types.Int64Value(int64(stats.Fills)),
		Invalidations:        types.Int64Value(int64(stats.Invalidations)),
		InflightFills:        types.Int64Value(int64(stats.InflightFills)),
		SingleflightJoins:    types.Int64Value(int64(stats.SingleflightJoins)),
		MemoryPressureEvents: types.Int64Value(int64(stats.MemoryPressureEvents)),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func init() {
	registerDataSource(NewObjectDataCacheStatsDataSource)
}
