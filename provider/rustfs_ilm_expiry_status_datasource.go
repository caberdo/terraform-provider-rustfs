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

var _ datasource.DataSource = &ILMExpiryStatusDataSource{}

type ILMExpiryStatusDataSource struct {
	client *AllClient
}

type ILMExpiryStatusDataSourceModel struct {
	ID                                 types.String `tfsdk:"id"`
	Enabled                            types.Bool   `tfsdk:"enabled"`
	DisabledReason                     types.String `tfsdk:"disabled_reason"`
	Freshness                          types.Object `tfsdk:"freshness"`
	LifecycleExpiry                    types.Object `tfsdk:"lifecycle_expiry"`
	MaintenanceControl                 types.Object `tfsdk:"maintenance_control"`
	CurrentCycleLifecycleExpiryActions types.Int64  `tfsdk:"current_cycle_lifecycle_expiry_actions"`
	LastCycleLifecycleExpiryActions    types.Int64  `tfsdk:"last_cycle_lifecycle_expiry_actions"`
	DataMovementPause                  types.Object `tfsdk:"data_movement_pause"`
	CatchUpEstimate                    types.Object `tfsdk:"catch_up_estimate"`
	JSON                               types.String `tfsdk:"json"`
}

var ilmFreshnessAttrTypes = map[string]attr.Type{
	"state":                    types.StringType,
	"last_cycle_end_unix_secs": types.Int64Type,
	"max_expected_age_seconds": types.Int64Type,
	"reason":                   types.StringType,
}

var ilmLifecycleExpiryAttrTypes = map[string]attr.Type{
	"current_queue_capacity": types.Int64Type,
	"current_queued":         types.Int64Type,
	"current_active":         types.Int64Type,
	"current_workers":        types.Int64Type,
	"queue_missed":           types.Int64Type,
	"scanner_queued":         types.Int64Type,
	"scanner_missed":         types.Int64Type,
	"scanner_blocked":        types.Int64Type,
	"scanner_not_enqueued":   types.Int64Type,
	"delete_failed":          types.Int64Type,
}

var ilmMaintenanceSourceAttrTypes = map[string]attr.Type{
	"source":          types.StringType,
	"state":           types.StringType,
	"reason":          types.StringType,
	"backlog":         types.Int64Type,
	"current_checked": types.Int64Type,
	"current_queued":  types.Int64Type,
	"current_missed":  types.Int64Type,
	"lifetime_missed": types.Int64Type,
	"partial_cycles":  types.Int64Type,
}

var ilmMaintenanceControlAttrTypes = map[string]attr.Type{
	"primary_control": types.StringType,
	"sources": types.ListType{
		ElemType: types.ObjectType{AttrTypes: ilmMaintenanceSourceAttrTypes},
	},
}

var ilmDataMovementPauseAttrTypes = map[string]attr.Type{
	"paused":                      types.BoolType,
	"policy":                      types.StringType,
	"reasons":                     types.ListType{ElemType: types.StringType},
	"started_at_unix_secs":        types.Int64Type,
	"duration_seconds":            types.Int64Type,
	"operation_epoch":             types.Int64Type,
	"movement_generation":         types.Int64Type,
	"movement_backlog_work_items": types.Int64Type,
	"movement_backlog_estimated":  types.BoolType,
}

var ilmCatchUpEstimateAttrTypes = map[string]attr.Type{
	"estimated":                    types.BoolType,
	"movement_work_items":          types.Int64Type,
	"dirty_usage_buckets":          types.Int64Type,
	"discovered_expiry_items":      types.Int64Type,
	"discovered_transition_items":  types.Int64Type,
	"undiscovered_ilm_items_known": types.BoolType,
	"usage_baseline_unix_secs":     types.Int64Type,
}

func NewILMExpiryStatusDataSource() datasource.DataSource {
	return &ILMExpiryStatusDataSource{}
}

func (d *ILMExpiryStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ilm_expiry_status"
}

func (d *ILMExpiryStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Exposes the RustFS lifecycle expiry worker status: whether expiry is enabled, how fresh the " +
		"scanner is, the expiry queue counters, maintenance control, data-movement pause and the scan catch-up " +
		"estimate. Read-only; it never changes ILM state."
	resp.Schema = schema.Schema{
		Description:         "Read the ILM expiry worker status",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"enabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the lifecycle expiry worker is enabled.",
			},
			"disabled_reason": schema.StringAttribute{
				Computed:    true,
				Description: "Why expiry is disabled, when the server reports a reason.",
			},
			"freshness": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Scanner freshness for the expiry worker.",
				Attributes: map[string]schema.Attribute{
					"state": schema.StringAttribute{
						Computed:    true,
						Description: "Freshness verdict: `fresh`, `stale` or `unknown`.",
					},
					"last_cycle_end_unix_secs": schema.Int64Attribute{
						Computed:    true,
						Description: "End of the last completed scanner cycle, in Unix seconds.",
					},
					"max_expected_age_seconds": schema.Int64Attribute{
						Computed:    true,
						Description: "Age after which the last cycle is considered stale, in seconds.",
					},
					"reason": schema.StringAttribute{
						Computed:    true,
						Description: "Why the scanner is not fresh, when the server reports a reason.",
					},
				},
			},
			"lifecycle_expiry": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Expiry worker queue and outcome counters.",
				Attributes: map[string]schema.Attribute{
					"current_queue_capacity": schema.Int64Attribute{
						Computed:    true,
						Description: "Expiry queue capacity.",
					},
					"current_queued": schema.Int64Attribute{
						Computed:    true,
						Description: "Objects waiting in the expiry queue.",
					},
					"current_active": schema.Int64Attribute{
						Computed:    true,
						Description: "Objects actively being expired.",
					},
					"current_workers": schema.Int64Attribute{
						Computed:    true,
						Description: "Expiry workers.",
					},
					"queue_missed": schema.Int64Attribute{
						Computed:    true,
						Description: "Expiry queue misses.",
					},
					"scanner_queued": schema.Int64Attribute{
						Computed:    true,
						Description: "Expiry items the scanner queued.",
					},
					"scanner_missed": schema.Int64Attribute{
						Computed:    true,
						Description: "Expiry items the scanner could not queue.",
					},
					"scanner_blocked": schema.Int64Attribute{
						Computed:    true,
						Description: "Expiry items blocked by the scanner.",
					},
					"scanner_not_enqueued": schema.Int64Attribute{
						Computed:    true,
						Description: "Expiry items the scanner skipped.",
					},
					"delete_failed": schema.Int64Attribute{
						Computed:    true,
						Description: "Expiry deletions that failed.",
					},
				},
			},
			"maintenance_control": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Which maintenance activity currently controls the scanner, and per-source state.",
				Attributes: map[string]schema.Attribute{
					"primary_control": schema.StringAttribute{
						Computed:    true,
						Description: "Maintenance activity currently controlling the scanner, or `none`.",
					},
					"sources": schema.ListNestedAttribute{
						Computed:    true,
						Description: "Per-source maintenance state.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"source": schema.StringAttribute{
									Computed:    true,
									Description: "Maintenance source name.",
								},
								"state": schema.StringAttribute{
									Computed:    true,
									Description: "Source state.",
								},
								"reason": schema.StringAttribute{
									Computed:    true,
									Description: "Reason for the state, when reported.",
								},
								"backlog": schema.Int64Attribute{
									Computed:    true,
									Description: "Outstanding work items for this source.",
								},
								"current_checked": schema.Int64Attribute{
									Computed:    true,
									Description: "Items checked in the current cycle.",
								},
								"current_queued": schema.Int64Attribute{
									Computed:    true,
									Description: "Items queued in the current cycle.",
								},
								"current_missed": schema.Int64Attribute{
									Computed:    true,
									Description: "Items missed in the current cycle.",
								},
								"lifetime_missed": schema.Int64Attribute{
									Computed:    true,
									Description: "Items missed since start.",
								},
								"partial_cycles": schema.Int64Attribute{
									Computed:    true,
									Description: "Cycles that did not complete.",
								},
							},
						},
					},
				},
			},
			"current_cycle_lifecycle_expiry_actions": schema.Int64Attribute{
				Computed:    true,
				Description: "Expiry actions taken in the current scanner cycle.",
			},
			"last_cycle_lifecycle_expiry_actions": schema.Int64Attribute{
				Computed:    true,
				Description: "Expiry actions taken in the last completed scanner cycle.",
			},
			"data_movement_pause": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Cluster-wide data-movement pause status.",
				Attributes: map[string]schema.Attribute{
					"paused": schema.BoolAttribute{
						Computed:    true,
						Description: "Whether data movement is paused.",
					},
					"policy": schema.StringAttribute{
						Computed:    true,
						Description: "Pause policy in force.",
					},
					"reasons": schema.ListAttribute{
						Computed:    true,
						ElementType: types.StringType,
						Description: "Why data movement is paused.",
					},
					"started_at_unix_secs": schema.Int64Attribute{
						Computed:    true,
						Description: "When the pause started, in Unix seconds.",
					},
					"duration_seconds": schema.Int64Attribute{
						Computed:    true,
						Description: "How long the pause has lasted, in seconds.",
					},
					"operation_epoch": schema.Int64Attribute{
						Computed:    true,
						Description: "Operation epoch at the time of the pause.",
					},
					"movement_generation": schema.Int64Attribute{
						Computed:    true,
						Description: "Movement generation at the time of the pause.",
					},
					"movement_backlog_work_items": schema.Int64Attribute{
						Computed:    true,
						Description: "Data-movement work items still outstanding.",
					},
					"movement_backlog_estimated": schema.BoolAttribute{
						Computed:    true,
						Description: "Whether the backlog figure is an estimate.",
					},
				},
			},
			"catch_up_estimate": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "The scanner's estimate of outstanding ILM work.",
				Attributes: map[string]schema.Attribute{
					"estimated": schema.BoolAttribute{
						Computed:    true,
						Description: "Whether the figures are estimates because work is paused or backlogged.",
					},
					"movement_work_items": schema.Int64Attribute{
						Computed:    true,
						Description: "Outstanding data-movement work items.",
					},
					"dirty_usage_buckets": schema.Int64Attribute{
						Computed:    true,
						Description: "Buckets whose usage accounting is dirty.",
					},
					"discovered_expiry_items": schema.Int64Attribute{
						Computed:    true,
						Description: "Expiry items discovered but not finished.",
					},
					"discovered_transition_items": schema.Int64Attribute{
						Computed:    true,
						Description: "Transition items discovered but not finished.",
					},
					"undiscovered_ilm_items_known": schema.BoolAttribute{
						Computed:    true,
						Description: "Whether undiscovered ILM items are known to exist.",
					},
					"usage_baseline_unix_secs": schema.Int64Attribute{
						Computed:    true,
						Description: "Last durable usage baseline, in Unix seconds.",
					},
				},
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON response as returned by the RustFS admin API.",
			},
		},
	}
}

func (d *ILMExpiryStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ILMExpiryStatusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ILMExpiryStatusDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := d.client.RustClient.ILMExpiryStatus(ctx)
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"ILM expiry status is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_ilm_expiry_status data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading ILM expiry status",
			"Could not read the ILM expiry status: "+err.Error(),
		)
		return
	}

	config.ID = types.StringValue("expiry-status")
	config.Enabled = types.BoolValue(status.Enabled)
	config.DisabledReason = nullableString(status.DisabledReason)
	config.Freshness = ilmFreshnessValue(status.Freshness)
	config.LifecycleExpiry = ilmLifecycleExpiryValue(status.LifecycleExpiry)
	config.MaintenanceControl = ilmMaintenanceControlValue(status.MaintenanceControl)
	config.CurrentCycleLifecycleExpiryActions = types.Int64Value(status.CurrentCycleLifecycleExpiryActions)
	config.LastCycleLifecycleExpiryActions = types.Int64Value(status.LastCycleLifecycleExpiryActions)
	config.DataMovementPause = ilmDataMovementPauseValue(status.DataMovementPause)
	config.CatchUpEstimate = ilmCatchUpEstimateValue(status.CatchUpEstimate)
	if len(status.Raw) > 0 {
		config.JSON = types.StringValue(string(status.Raw))
	} else {
		config.JSON = types.StringValue("{}")
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func ilmFreshnessValue(freshness rustfs.ILMFreshness) types.Object {
	return types.ObjectValueMust(ilmFreshnessAttrTypes, map[string]attr.Value{
		"state":                    types.StringValue(freshness.State),
		"last_cycle_end_unix_secs": types.Int64Value(freshness.LastCycleEndUnixSecs),
		"max_expected_age_seconds": types.Int64Value(freshness.MaxExpectedAgeSeconds),
		"reason":                   nullableString(freshness.Reason),
	})
}

func ilmLifecycleExpiryValue(counters rustfs.ILMLifecycleExpiryCounters) types.Object {
	return types.ObjectValueMust(ilmLifecycleExpiryAttrTypes, map[string]attr.Value{
		"current_queue_capacity": types.Int64Value(counters.CurrentQueueCapacity),
		"current_queued":         types.Int64Value(counters.CurrentQueued),
		"current_active":         types.Int64Value(counters.CurrentActive),
		"current_workers":        types.Int64Value(counters.CurrentWorkers),
		"queue_missed":           types.Int64Value(counters.QueueMissed),
		"scanner_queued":         types.Int64Value(counters.ScannerQueued),
		"scanner_missed":         types.Int64Value(counters.ScannerMissed),
		"scanner_blocked":        types.Int64Value(counters.ScannerBlocked),
		"scanner_not_enqueued":   types.Int64Value(counters.ScannerNotEnqueued),
		"delete_failed":          types.Int64Value(counters.DeleteFailed),
	})
}

func ilmMaintenanceControlValue(control rustfs.ILMMaintenanceControl) types.Object {
	sources := make([]attr.Value, 0, len(control.Sources))
	for _, source := range control.Sources {
		sources = append(sources, types.ObjectValueMust(ilmMaintenanceSourceAttrTypes, map[string]attr.Value{
			"source":          types.StringValue(source.Source),
			"state":           types.StringValue(source.State),
			"reason":          nullableString(source.Reason),
			"backlog":         types.Int64Value(source.Backlog),
			"current_checked": types.Int64Value(source.CurrentChecked),
			"current_queued":  types.Int64Value(source.CurrentQueued),
			"current_missed":  types.Int64Value(source.CurrentMissed),
			"lifetime_missed": types.Int64Value(source.LifetimeMissed),
			"partial_cycles":  types.Int64Value(source.PartialCycles),
		}))
	}
	return types.ObjectValueMust(ilmMaintenanceControlAttrTypes, map[string]attr.Value{
		"primary_control": types.StringValue(control.PrimaryControl),
		"sources":         types.ListValueMust(types.ObjectType{AttrTypes: ilmMaintenanceSourceAttrTypes}, sources),
	})
}

func ilmDataMovementPauseValue(pause rustfs.ILMDataMovementPause) types.Object {
	reasons := make([]attr.Value, 0, len(pause.Reasons))
	for _, reason := range pause.Reasons {
		reasons = append(reasons, types.StringValue(reason))
	}
	return types.ObjectValueMust(ilmDataMovementPauseAttrTypes, map[string]attr.Value{
		"paused":                      types.BoolValue(pause.Paused),
		"policy":                      types.StringValue(pause.Policy),
		"reasons":                     types.ListValueMust(types.StringType, reasons),
		"started_at_unix_secs":        types.Int64Value(pause.StartedAtUnixSecs),
		"duration_seconds":            types.Int64Value(pause.DurationSeconds),
		"operation_epoch":             types.Int64Value(pause.OperationEpoch),
		"movement_generation":         types.Int64Value(pause.MovementGeneration),
		"movement_backlog_work_items": types.Int64Value(pause.MovementBacklogWorkItems),
		"movement_backlog_estimated":  types.BoolValue(pause.MovementBacklogEstimated),
	})
}

func ilmCatchUpEstimateValue(estimate rustfs.ILMCatchUpEstimate) types.Object {
	return types.ObjectValueMust(ilmCatchUpEstimateAttrTypes, map[string]attr.Value{
		"estimated":                    types.BoolValue(estimate.Estimated),
		"movement_work_items":          types.Int64Value(estimate.MovementWorkItems),
		"dirty_usage_buckets":          types.Int64Value(estimate.DirtyUsageBuckets),
		"discovered_expiry_items":      types.Int64Value(estimate.DiscoveredExpiryItems),
		"discovered_transition_items":  types.Int64Value(estimate.DiscoveredTransitionItems),
		"undiscovered_ilm_items_known": types.BoolValue(estimate.UndiscoveredILMItemsKnown),
		"usage_baseline_unix_secs":     types.Int64Value(estimate.UsageBaselineUnixSecs),
	})
}

// nullableString maps the server's empty-or-absent string fields to null so a
// missing reason reads as null rather than as an empty string.
func nullableString(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

func init() {
	registerDataSource(NewILMExpiryStatusDataSource)
}
