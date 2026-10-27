// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ReplicationMrfDataSource{}

type ReplicationMrfDataSource struct {
	client *AllClient
}

type ReplicationMrfDataSourceModel struct {
	Bucket                           types.String `tfsdk:"bucket"`
	ID                               types.String `tfsdk:"id"`
	Targets                          types.List   `tfsdk:"targets"`
	TotalFailedCount                 types.Int64  `tfsdk:"total_failed_count"`
	TotalFailedSize                  types.Int64  `tfsdk:"total_failed_size"`
	QueuedCount                      types.Int64  `tfsdk:"queued_count"`
	QueuedSize                       types.Int64  `tfsdk:"queued_size"`
	PerObjectEntriesAvailable        types.Bool   `tfsdk:"per_object_entries_available"`
	RuntimeStatsAvailable            types.Bool   `tfsdk:"runtime_stats_available"`
	ClusterComplete                  types.Bool   `tfsdk:"cluster_complete"`
	ObservedNodeCount                types.Int64  `tfsdk:"observed_node_count"`
	ExpectedNodeCount                types.Int64  `tfsdk:"expected_node_count"`
	DurableBacklogAvailable          types.Bool   `tfsdk:"durable_backlog_available"`
	DurableCount                     types.Int64  `tfsdk:"durable_count"`
	DurableSize                      types.Int64  `tfsdk:"durable_size"`
	PerTargetDurableEntriesAvailable types.Bool   `tfsdk:"per_target_durable_entries_available"`
	ReplicationConfigured            types.Bool   `tfsdk:"replication_configured"`
	JSON                             types.String `tfsdk:"json"`
}

var replicationMrfTargetAttrTypes = map[string]attr.Type{
	"arn":               types.StringType,
	"failed_count":      types.Int64Type,
	"failed_size":       types.Int64Type,
	"durable_count":     types.Int64Type,
	"durable_size":      types.Int64Type,
	"observation_scope": types.StringType,
}

func NewReplicationMrfDataSource() datasource.DataSource {
	return &ReplicationMrfDataSource{}
}

func (d *ReplicationMrfDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_replication_mrf"
}

func (d *ReplicationMrfDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Reads the failed-replication (most-recent-failure) backlog counters of a bucket. The counters " +
		"come from the aggregate response shape, which carries no object names or version ids and only needs the " +
		"narrow `GetReplicationMetrics` admin action; the default madmin stream enumerates object identities and " +
		"needs `ReplicationDiff`. The durable ledger is cluster-shared, so the counters cover the whole cluster. " +
		"This data source is read-only: MRF records are operational data, not configuration, and it never retries " +
		"or drains them."
	resp.Schema = schema.Schema{
		Description:         "Read the replication MRF backlog of a bucket",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Bucket whose replication MRF backlog to read.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier, derived from the bucket.",
			},
			"targets": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Per-target failed and durable backlog counters, sorted by ARN.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"arn": schema.StringAttribute{
							Computed:    true,
							Description: "Remote target ARN.",
						},
						"failed_count": schema.Int64Attribute{
							Computed:    true,
							Description: "Objects whose replication to this target failed.",
						},
						"failed_size": schema.Int64Attribute{
							Computed:    true,
							Description: "Bytes whose replication to this target failed.",
						},
						"durable_count": schema.Int64Attribute{
							Computed:    true,
							Description: "Durable MRF ledger entries attributed to this target.",
						},
						"durable_size": schema.Int64Attribute{
							Computed:    true,
							Description: "Bytes in durable MRF ledger entries attributed to this target.",
						},
						"observation_scope": schema.StringAttribute{
							Computed: true,
							Description: "Whether the counters are `cluster_aggregated` or only " +
								"`partial_cluster` observations.",
						},
					},
				},
			},
			"total_failed_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Failed replication operations across every target.",
			},
			"total_failed_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Bytes that failed to replicate across every target.",
			},
			"queued_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Replication operations currently queued.",
			},
			"queued_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Bytes currently queued for replication.",
			},
			"per_object_entries_available": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the durable per-object backlog can be enumerated.",
			},
			"runtime_stats_available": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether runtime replication statistics were readable.",
			},
			"cluster_complete": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether every expected node contributed to the counters.",
			},
			"observed_node_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Nodes that contributed to the counters.",
			},
			"expected_node_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Nodes expected to contribute to the counters.",
			},
			"durable_backlog_available": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the durable MRF ledger was readable.",
			},
			"durable_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Entries in the durable MRF ledger for this bucket.",
			},
			"durable_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Bytes in the durable MRF ledger for this bucket.",
			},
			"per_target_durable_entries_available": schema.BoolAttribute{
				Computed: true,
				Description: "Whether every durable entry could be attributed to a target. Older entries may " +
					"carry no ARN.",
			},
			"replication_configured": schema.BoolAttribute{
				Computed: true,
				Description: "Whether the bucket has a replication configuration. When false the counters are " +
					"zero and no backlog was read; the server reports ReplicationConfigurationNotFoundError.",
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON aggregate response as returned by the RustFS admin API.",
			},
		},
	}
}

func (d *ReplicationMrfDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ReplicationMrfDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ReplicationMrfDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := config.Bucket.ValueString()
	if bucket == "" {
		resp.Diagnostics.AddError(
			"Error reading replication MRF backlog",
			"bucket is required",
		)
		return
	}

	mrf, err := d.client.RustClient.ReplicationMRF(ctx, bucket)
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"Replication MRF backlog is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_replication_mrf data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading replication MRF backlog",
			"Could not read the replication MRF backlog for bucket "+bucket+": "+err.Error(),
		)
		return
	}

	config.ID = types.StringValue("replication-mrf/" + bucket)
	config.Targets = replicationMrfTargetsValue(mrf.Targets)
	config.TotalFailedCount = types.Int64Value(mrf.TotalFailedCount)
	config.TotalFailedSize = types.Int64Value(mrf.TotalFailedSize)
	config.QueuedCount = types.Int64Value(mrf.QueuedCount)
	config.QueuedSize = types.Int64Value(mrf.QueuedSize)
	config.PerObjectEntriesAvailable = types.BoolValue(mrf.PerObjectEntriesAvailable)
	config.RuntimeStatsAvailable = types.BoolValue(mrf.RuntimeStatsAvailable)
	config.ClusterComplete = types.BoolValue(mrf.ClusterComplete)
	config.ObservedNodeCount = types.Int64Value(int64(mrf.ObservedNodeCount))
	config.ExpectedNodeCount = types.Int64Value(int64(mrf.ExpectedNodeCount))
	config.DurableBacklogAvailable = types.BoolValue(mrf.DurableBacklogAvailable)
	config.DurableCount = types.Int64Value(mrf.DurableCount)
	config.DurableSize = types.Int64Value(mrf.DurableSize)
	config.PerTargetDurableEntriesAvailable = types.BoolValue(mrf.PerTargetDurableEntriesAvailable)
	config.ReplicationConfigured = types.BoolValue(mrf.ReplicationConfigured)
	if len(mrf.Raw) > 0 {
		config.JSON = types.StringValue(string(mrf.Raw))
	} else {
		config.JSON = types.StringValue("{}")
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func replicationMrfTargetsValue(targets []rustfs.ReplicationMRFTarget) types.List {
	values := make([]attr.Value, 0, len(targets))
	for _, target := range targets {
		values = append(values, types.ObjectValueMust(replicationMrfTargetAttrTypes, map[string]attr.Value{
			"arn":               types.StringValue(target.ARN),
			"failed_count":      types.Int64Value(target.FailedCount),
			"failed_size":       types.Int64Value(target.FailedSize),
			"durable_count":     types.Int64Value(target.DurableCount),
			"durable_size":      types.Int64Value(target.DurableSize),
			"observation_scope": types.StringValue(target.ObservationScope),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: replicationMrfTargetAttrTypes}, values)
}

func init() {
	registerDataSource(NewReplicationMrfDataSource)
}
