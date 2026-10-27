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
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &bucketOnDemandMigrationStatusDataSource{}

func NewBucketOnDemandMigrationStatusDataSource() datasource.DataSource {
	return &bucketOnDemandMigrationStatusDataSource{}
}

// bucketOnDemandMigrationStatusDataSource reads the per-node runtime snapshot
// of a bucket's on-demand migration configuration. In a distributed deployment
// its counters, queue depth and breaker describe only the node that answered.
type bucketOnDemandMigrationStatusDataSource struct {
	client *AllClient
}

type bucketOnDemandMigrationStatusModel struct {
	Bucket              types.String             `tfsdk:"bucket"`
	Configured          types.Bool               `tfsdk:"configured"`
	Enabled             types.Bool               `tfsdk:"enabled"`
	ModuleEnabled       types.Bool               `tfsdk:"module_enabled"`
	SourceProvider      types.String             `tfsdk:"source_provider"`
	EndpointHost        types.String             `tfsdk:"endpoint_host"`
	Breaker             *odmBreakerStatusModel   `tfsdk:"breaker"`
	Counters            *odmCountersModel        `tfsdk:"counters"`
	LastSourceError     *odmLastSourceErrorModel `tfsdk:"last_source_error"`
	InflightPulls       types.Int64              `tfsdk:"inflight_pulls"`
	QueueDepth          types.Int64              `tfsdk:"queue_depth"`
	ServedBySourceRatio types.Float64            `tfsdk:"served_by_source_ratio"`
	UpdatedAt           types.String             `tfsdk:"updated_at"`
	Backfill            *odmBackfillSummaryModel `tfsdk:"backfill"`
}

type odmBreakerStatusModel struct {
	State    types.String `tfsdk:"state"`
	OpenedAt types.String `tfsdk:"opened_at"`
}

type odmCountersModel struct {
	RequestsTotal      types.Map              `tfsdk:"requests_total"`
	PulledBytesTotal   types.Int64            `tfsdk:"pulled_bytes_total"`
	PulledObjectsTotal types.Map              `tfsdk:"pulled_objects_total"`
	PullFailuresTotal  types.Map              `tfsdk:"pull_failures_total"`
	SourceLatency      *odmSourceLatencyModel `tfsdk:"source_latency"`
}

type odmSourceLatencyModel struct {
	Buckets types.List  `tfsdk:"buckets"`
	Count   types.Int64 `tfsdk:"count"`
	SumMS   types.Int64 `tfsdk:"sum_ms"`
}

type odmlatencyBucketModel struct {
	LeMS  types.Int64 `tfsdk:"le_ms"`
	Count types.Int64 `tfsdk:"count"`
}

type odmLastSourceErrorModel struct {
	Class types.String `tfsdk:"class"`
	At    types.String `tfsdk:"at"`
}

type odmBackfillSummaryModel struct {
	JobID           types.String `tfsdk:"job_id"`
	State           types.String `tfsdk:"state"`
	Listed          types.Int64  `tfsdk:"listed"`
	Enqueued        types.Int64  `tfsdk:"enqueued"`
	Pulled          types.Int64  `tfsdk:"pulled"`
	SkippedExisting types.Int64  `tfsdk:"skipped_existing"`
	Failed          types.Int64  `tfsdk:"failed"`
	Bytes           types.Int64  `tfsdk:"bytes"`
	UpdatedAt       types.String `tfsdk:"updated_at"`
}

func (d *bucketOnDemandMigrationStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_on_demand_migration_status"
}

func (d *bucketOnDemandMigrationStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Read a bucket's on-demand migration runtime status",
		MarkdownDescription: "Read the runtime status of a bucket's on-demand migration configuration " +
			"(`GET /rustfs/admin/v3/on-demand-migration/{bucket}/status`).\n\n" +
			"**Per-node caveat:** this endpoint reports the node that answered the request. Counters, queue " +
			"depth and breaker state are per-node runtime state, so in a distributed deployment query every node " +
			"to build a cluster-wide picture. The saved configuration and `updated_at` are cluster-wide; the " +
			"provider reads whichever node its endpoint resolves to.\n\n" +
			"Reads of the status endpoint keep working while the module switch is off, so a disabled deployment " +
			"can still be inspected.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the local bucket to inspect.",
			},
			"configured": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the bucket has a saved on-demand migration configuration.",
			},
			"enabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether source traffic is enabled for the saved configuration.",
			},
			"module_enabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the node that answered has the RUSTFS_ON_DEMAND_MIGRATION_ENABLED module switch on.",
			},
			"source_provider": schema.StringAttribute{
				Computed:    true,
				Description: "Configured source provider, when configured.",
			},
			"endpoint_host": schema.StringAttribute{
				Computed:    true,
				Description: "Host of the configured source endpoint, when configured.",
			},
			"breaker": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Per-node circuit breaker state. Null while the bucket has no live state on this node.",
				Attributes: map[string]schema.Attribute{
					"state": schema.StringAttribute{
						Computed:    true,
						Description: "Breaker state, for example `closed`, `open` or `half_open`.",
					},
					"opened_at": schema.StringAttribute{
						Computed:    true,
						Description: "When the breaker opened, if the server reports it (currently always null).",
					},
				},
			},
			"counters": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Lifetime counters of this node's bucket runtime. Null while the bucket has no live state on this node.",
				Attributes: map[string]schema.Attribute{
					"requests_total": schema.MapAttribute{
						Computed:    true,
						ElementType: types.MapType{ElemType: types.Int64Type},
						Description: "Requests by operation and outcome, keyed like the Prometheus series labels.",
					},
					"pulled_bytes_total": schema.Int64Attribute{
						Computed:    true,
						Description: "Total bytes pulled from the source on this node.",
					},
					"pulled_objects_total": schema.MapAttribute{
						Computed:    true,
						ElementType: types.Int64Type,
						Description: "Pulled objects by outcome.",
					},
					"pull_failures_total": schema.MapAttribute{
						Computed:    true,
						ElementType: types.Int64Type,
						Description: "Failed pulls by reason.",
					},
					"source_latency": schema.SingleNestedAttribute{
						Computed:    true,
						Description: "Histogram snapshot of source response time on this node.",
						Attributes: map[string]schema.Attribute{
							"buckets": schema.ListNestedAttribute{
								Computed:    true,
								Description: "Cumulative histogram buckets.",
								NestedObject: schema.NestedAttributeObject{
									Attributes: map[string]schema.Attribute{
										"le_ms": schema.Int64Attribute{
											Computed:    true,
											Description: "Upper bound of the bucket in milliseconds.",
										},
										"count": schema.Int64Attribute{
											Computed:    true,
											Description: "Cumulative observations at or below the bound.",
										},
									},
								},
							},
							"count": schema.Int64Attribute{
								Computed:    true,
								Description: "Number of observations.",
							},
							"sum_ms": schema.Int64Attribute{
								Computed:    true,
								Description: "Sum of observed latency in milliseconds.",
							},
						},
					},
				},
			},
			"last_source_error": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Last classified source failure seen on this node.",
				Attributes: map[string]schema.Attribute{
					"class": schema.StringAttribute{
						Computed:    true,
						Description: "Error class label.",
					},
					"at": schema.StringAttribute{
						Computed:    true,
						Description: "RFC 3339 time of the failure.",
					},
				},
			},
			"inflight_pulls": schema.Int64Attribute{
				Computed:    true,
				Description: "Pulls currently in flight on this node.",
			},
			"queue_depth": schema.Int64Attribute{
				Computed:    true,
				Description: "Background pull jobs queued on this node.",
			},
			"served_by_source_ratio": schema.Float64Attribute{
				Computed:    true,
				Description: "Ratio of source-served reads, when the server can compute it.",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "RFC 3339 save time of the configuration, when configured.",
			},
			"backfill": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Latest backfill job of the bucket. Null when none was ever started. Backfill is not managed by this provider.",
				Attributes: map[string]schema.Attribute{
					"job_id": schema.StringAttribute{
						Computed:    true,
						Description: "Backfill job identifier.",
					},
					"state": schema.StringAttribute{
						Computed:    true,
						Description: "Job state. `completed_with_failures` is not a cutover-ready state.",
					},
					"listed": schema.Int64Attribute{
						Computed:    true,
						Description: "Source objects listed.",
					},
					"enqueued": schema.Int64Attribute{
						Computed:    true,
						Description: "Objects enqueued for pulling.",
					},
					"pulled": schema.Int64Attribute{
						Computed:    true,
						Description: "Objects pulled.",
					},
					"skipped_existing": schema.Int64Attribute{
						Computed:    true,
						Description: "Objects skipped because they already existed locally.",
					},
					"failed": schema.Int64Attribute{
						Computed:    true,
						Description: "Objects that failed to pull.",
					},
					"bytes": schema.Int64Attribute{
						Computed:    true,
						Description: "Bytes pulled by the job.",
					},
					"updated_at": schema.StringAttribute{
						Computed:    true,
						Description: "RFC 3339 time the checkpoint was last updated.",
					},
				},
			},
		},
	}
}

func (d *bucketOnDemandMigrationStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketOnDemandMigrationStatusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config bucketOnDemandMigrationStatusModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := config.Bucket.ValueString()
	status, err := d.client.RustClient.GetBucketOnDemandMigrationStatus(ctx, bucket)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading bucket on-demand migration status",
			"Could not read on-demand migration status for bucket "+bucket+": "+odmErrorText(err),
		)
		return
	}

	resp.Diagnostics.Append(applyODMStatus(ctx, &config, status)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func applyODMStatus(ctx context.Context, model *bucketOnDemandMigrationStatusModel, status rustfs.ODMStatus) (diags diag.Diagnostics) {
	model.Configured = types.BoolValue(status.Configured)
	model.Enabled = types.BoolValue(status.Enabled)
	model.ModuleEnabled = types.BoolValue(status.ModuleEnabled)
	model.SourceProvider = stringValueOrNull(status.Provider)
	model.EndpointHost = stringValueOrNull(status.EndpointHost)
	model.InflightPulls = types.Int64Value(int64(status.InflightPulls))
	model.QueueDepth = types.Int64Value(int64(status.QueueDepth))
	model.UpdatedAt = stringValueOrNull(status.UpdatedAt)

	if status.ServedBySourceRatio == nil {
		model.ServedBySourceRatio = types.Float64Null()
	} else {
		model.ServedBySourceRatio = types.Float64Value(*status.ServedBySourceRatio)
	}

	if status.Breaker == nil {
		model.Breaker = nil
	} else {
		model.Breaker = &odmBreakerStatusModel{
			State:    types.StringValue(status.Breaker.State),
			OpenedAt: stringValueOrNull(status.Breaker.OpenedAt),
		}
	}

	if status.LastSourceError == nil {
		model.LastSourceError = nil
	} else {
		model.LastSourceError = &odmLastSourceErrorModel{
			Class: types.StringValue(status.LastSourceError.Class),
			At:    types.StringValue(status.LastSourceError.At),
		}
	}

	model.Counters = nil
	if status.Counters != nil {
		counters := &odmCountersModel{
			PulledBytesTotal: types.Int64Value(int64(status.Counters.PulledBytesTotal)),
		}

		requestsType := types.MapType{ElemType: types.Int64Type}
		requests, requestDiags := types.MapValueFrom(ctx, requestsType, status.Counters.RequestsTotal)
		diags = append(diags, requestDiags...)
		counters.RequestsTotal = requests

		pulled, pulledDiags := types.MapValueFrom(ctx, types.Int64Type, status.Counters.PulledObjectsTotal)
		diags = append(diags, pulledDiags...)
		counters.PulledObjectsTotal = pulled

		failures, failureDiags := types.MapValueFrom(ctx, types.Int64Type, status.Counters.PullFailuresTotal)
		diags = append(diags, failureDiags...)
		counters.PullFailuresTotal = failures

		latency := &odmSourceLatencyModel{
			Count: types.Int64Value(int64(status.Counters.SourceLatency.Count)),
			SumMS: types.Int64Value(int64(status.Counters.SourceLatency.SumMS)),
		}
		buckets := make([]odmlatencyBucketModel, 0, len(status.Counters.SourceLatency.Buckets))
		for _, entry := range status.Counters.SourceLatency.Buckets {
			buckets = append(buckets, odmlatencyBucketModel{
				LeMS:  types.Int64Value(int64(entry.LeMS)),
				Count: types.Int64Value(int64(entry.Count)),
			})
		}
		bucketList, bucketDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: odmlatencyBucketAttrTypes()}, buckets)
		diags = append(diags, bucketDiags...)
		latency.Buckets = bucketList
		counters.SourceLatency = latency
		model.Counters = counters
	}

	if status.Backfill == nil {
		model.Backfill = nil
	} else {
		model.Backfill = &odmBackfillSummaryModel{
			JobID:           types.StringValue(status.Backfill.JobID),
			State:           types.StringValue(status.Backfill.State),
			Listed:          types.Int64Value(int64(status.Backfill.Listed)),
			Enqueued:        types.Int64Value(int64(status.Backfill.Enqueued)),
			Pulled:          types.Int64Value(int64(status.Backfill.Pulled)),
			SkippedExisting: types.Int64Value(int64(status.Backfill.SkippedExisting)),
			Failed:          types.Int64Value(int64(status.Backfill.Failed)),
			Bytes:           types.Int64Value(int64(status.Backfill.Bytes)),
			UpdatedAt:       types.StringValue(status.Backfill.UpdatedAt),
		}
	}

	return diags
}

func odmlatencyBucketAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"le_ms": types.Int64Type,
		"count": types.Int64Type,
	}
}

func init() {
	registerDataSource(NewBucketOnDemandMigrationStatusDataSource)
}
