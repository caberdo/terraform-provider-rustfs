// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &QuotaStatsDataSource{}

// QuotaStatsDataSource reads the quota subsystem's view of one bucket from
// GET /rustfs/admin/v3/quota-stats/{bucket}.
type QuotaStatsDataSource struct {
	client *AllClient
}

type QuotaStatsDataSourceModel struct {
	ID              types.String  `tfsdk:"id"`
	Bucket          types.String  `tfsdk:"bucket"`
	QuotaLimit      types.Int64   `tfsdk:"quota_limit"`
	CurrentUsage    types.Int64   `tfsdk:"current_usage"`
	RemainingQuota  types.Int64   `tfsdk:"remaining_quota"`
	UsagePercentage types.Float64 `tfsdk:"usage_percentage"`
}

func NewQuotaStatsDataSource() datasource.DataSource {
	return &QuotaStatsDataSource{}
}

func (d *QuotaStatsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_quota_stats"
}

func (d *QuotaStatsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Read the quota subsystem's view of one bucket (GET /rustfs/admin/v3/quota-stats/{bucket}). " +
		"This differs from `rustfs_bucket_usage`: `rustfs_bucket_usage` reports the scanner's eventually-consistent " +
		"accounting (cached for up to 30 seconds, with `sets_reporting`/`sets_total` telling you whether the " +
		"answer is complete), while `rustfs_quota_stats` reports the quota subsystem's own view, whose usage may be " +
		"reservation/ledger based. A field the server cannot report is null, never zero. A missing bucket fails " +
		"the read with the server error instead of reporting zeros."
	resp.Schema = schema.Schema{
		Description:         "Read a bucket's quota limit, usage and remaining headroom",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier, the bucket name that was read.",
			},
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket to read the quota statistics for.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"quota_limit": schema.Int64Attribute{
				Computed: true,
				Description: "Configured hard quota in bytes. Null when the bucket has no quota configured; the " +
					"server reports no over-quota flag, so compare usage against this value yourself.",
			},
			"current_usage": schema.Int64Attribute{
				Computed: true,
				Description: "Usage in bytes as the quota subsystem sees it. Null when the server could not " +
					"report a usage figure (for example a bucket admitted only by the reservation ledger), never " +
					"silently zero.",
			},
			"remaining_quota": schema.Int64Attribute{
				Computed:    true,
				Description: "Bytes still allowed before the quota is reached. Null when the server could not report it, for example when no quota is configured.",
			},
			"usage_percentage": schema.Float64Attribute{
				Computed:    true,
				Description: "Usage as a percentage of the quota. Null when either the usage or the quota is unknown or the quota is zero.",
			},
		},
	}
}

func (d *QuotaStatsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *QuotaStatsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config QuotaStatsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := config.Bucket.ValueString()

	stats, err := d.client.RustClient.GetQuotaStats(bucket)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading bucket quota statistics",
			"Could not read quota statistics for bucket "+bucket+": "+err.Error(),
		)
		return
	}

	config.ID = types.StringValue(bucket)
	config.QuotaLimit = uint64NullInt64(stats.QuotaLimit)
	config.CurrentUsage = uint64NullInt64(stats.CurrentUsage)
	config.RemainingQuota = uint64NullInt64(stats.RemainingQuota)
	if stats.UsagePercentage == nil {
		config.UsagePercentage = types.Float64Null()
	} else {
		config.UsagePercentage = types.Float64Value(*stats.UsagePercentage)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// uint64NullInt64 keeps a value the server did not report null instead of
// turning it into zero, which is a real answer for usage.
func uint64NullInt64(value *uint64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*value))
}

func init() {
	registerDataSource(NewQuotaStatsDataSource)
}
