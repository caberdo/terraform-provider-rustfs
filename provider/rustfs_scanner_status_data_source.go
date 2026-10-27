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

var _ datasource.DataSource = &ScannerStatusDataSource{}

type ScannerStatusDataSource struct {
	client *AllClient
}

type ScannerStatusDataSourceModel struct {
	ID                              types.String  `tfsdk:"id"`
	StatusJSON                      types.String  `tfsdk:"status_json"`
	Enabled                         types.Bool    `tfsdk:"enabled"`
	DisabledReason                  types.String  `tfsdk:"disabled_reason"`
	FreshnessState                  types.String  `tfsdk:"freshness_state"`
	CurrentCycle                    types.Int64   `tfsdk:"current_cycle"`
	CurrentCycleActive              types.Bool    `tfsdk:"current_cycle_active"`
	CurrentScanMode                 types.String  `tfsdk:"current_scan_mode"`
	LastCycleEndUnixSecs            types.Int64   `tfsdk:"last_cycle_end_unix_secs"`
	LastCycleResult                 types.String  `tfsdk:"last_cycle_result"`
	LastCycleDurationSeconds        types.Float64 `tfsdk:"last_cycle_duration_seconds"`
	LastCycleObjectsScanned         types.Int64   `tfsdk:"last_cycle_objects_scanned"`
	LastCycleDirectoriesScanned     types.Int64   `tfsdk:"last_cycle_directories_scanned"`
	CyclesCompleted                 types.Int64   `tfsdk:"cycles_completed"`
	FailedCycles                    types.Int64   `tfsdk:"failed_cycles"`
	SupersededCycles                types.Int64   `tfsdk:"superseded_cycles"`
	EffectiveCycleIntervalSeconds   types.Int64   `tfsdk:"effective_cycle_interval_seconds"`
	UsageBootstrapRebuildPending    types.Bool    `tfsdk:"usage_bootstrap_rebuild_pending"`
	UsagePublicationState           types.String  `tfsdk:"usage_publication_state"`
	UsageLastDurableSuccessUnixSecs types.Int64   `tfsdk:"usage_last_durable_success_unix_secs"`
	UsageDirtyPendingBuckets        types.Int64   `tfsdk:"usage_dirty_pending_buckets"`
	MaintenancePrimaryControl       types.String  `tfsdk:"maintenance_primary_control"`
	CycleRecoveryState              types.String  `tfsdk:"cycle_recovery_state"`
	CycleRecoveryReason             types.String  `tfsdk:"cycle_recovery_reason"`
	DataMovementPaused              types.Bool    `tfsdk:"data_movement_paused"`
	PauseBacklogPhase               types.String  `tfsdk:"pause_backlog_phase"`
}

func NewScannerStatusDataSource() datasource.DataSource {
	return &ScannerStatusDataSource{}
}

func (d *ScannerStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scanner_status"
}

func (d *ScannerStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Runtime status of the RustFS object scanner (GET /rustfs/admin/v3/scanner/status): " +
		"cycle state, the last completed cycle and its counters, usage publication state, and " +
		"data-movement pause state. The scanner's tuning knobs stay in the `scanner` subsystem of " +
		"`rustfs_config` or in the server environment; this data source only observes them."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"status_json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON response as returned by the RustFS admin API.",
			},
			"enabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the scanner is enabled on this server.",
			},
			"disabled_reason": schema.StringAttribute{
				Computed:    true,
				Description: "Why the scanner is disabled, when it is.",
			},
			"freshness_state": schema.StringAttribute{
				Computed:    true,
				Description: "Scanner freshness: `fresh`, `stale` or `unknown`.",
			},
			"current_cycle": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of the running or most recent scanner cycle.",
			},
			"current_cycle_active": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether a scanner cycle is running.",
			},
			"current_scan_mode": schema.StringAttribute{
				Computed:    true,
				Description: "Scan mode of the running or most recent cycle, `normal` or `deep`.",
			},
			"last_cycle_end_unix_secs": schema.Int64Attribute{
				Computed:    true,
				Description: "Unix time at which the last cycle ended, or `0` before the first completed cycle.",
			},
			"last_cycle_result": schema.StringAttribute{
				Computed:    true,
				Description: "Result of the last cycle, for example `success`, `partial`, `superseded` or `error`.",
			},
			"last_cycle_duration_seconds": schema.Float64Attribute{
				Computed:    true,
				Description: "Wall-clock duration of the last cycle in seconds.",
			},
			"last_cycle_objects_scanned": schema.Int64Attribute{
				Computed:    true,
				Description: "Object versions the last cycle scanned.",
			},
			"last_cycle_directories_scanned": schema.Int64Attribute{
				Computed:    true,
				Description: "Directories the last cycle scanned.",
			},
			"cycles_completed": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of cycle completions retained in the status window.",
			},
			"failed_cycles": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of failed scanner cycles since startup.",
			},
			"superseded_cycles": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of cycles superseded by newer writes since startup.",
			},
			"effective_cycle_interval_seconds": schema.Int64Attribute{
				Computed:    true,
				Description: "Effective interval between scanner cycles, including clean-idle backoff.",
			},
			"usage_bootstrap_rebuild_pending": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether a usage-state full rebuild is still pending after a reset.",
			},
			"usage_publication_state": schema.StringAttribute{
				Computed:    true,
				Description: "State of the last scanner usage publication.",
			},
			"usage_last_durable_success_unix_secs": schema.Int64Attribute{
				Computed:    true,
				Description: "Unix time of the last durable usage publication, or `0` when none is recorded.",
			},
			"usage_dirty_pending_buckets": schema.Int64Attribute{
				Computed:    true,
				Description: "Buckets whose usage is marked dirty and waiting for a scanner pass.",
			},
			"maintenance_primary_control": schema.StringAttribute{
				Computed:    true,
				Description: "Primary reason scanner maintenance work is blocked, deferred or paced, or `none`.",
			},
			"cycle_recovery_state": schema.StringAttribute{
				Computed:    true,
				Description: "Scanner cycle-recovery state on this node, for example `healthy` or `recovery-required`.",
			},
			"cycle_recovery_reason": schema.StringAttribute{
				Computed:    true,
				Description: "Reason recorded with the cycle-recovery state, when one is present.",
			},
			"data_movement_paused": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether a pool decommission or rebalance currently pauses scanner metadata publication.",
			},
			"pause_backlog_phase": schema.StringAttribute{
				Computed:    true,
				Description: "Catch-up phase of the pause ledger: `idle`, `paused`, `catching_up` or `retry_exhausted`.",
			},
		},
	}
}

func (d *ScannerStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ScannerStatusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	status, err := d.client.RustClient.GetScannerStatus(ctx)
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"Scanner status is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_scanner_status data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading scanner status",
			"Could not read scanner status, unexpected error: "+err.Error(),
		)
		return
	}

	state := ScannerStatusDataSourceModel{
		ID:                              types.StringValue("scanner-status"),
		StatusJSON:                      types.StringValue(status.RawJSON),
		Enabled:                         types.BoolValue(status.Enabled),
		DisabledReason:                  types.StringPointerValue(status.DisabledReason),
		FreshnessState:                  types.StringValue(status.FreshnessState),
		CurrentCycle:                    types.Int64Value(int64(status.CurrentCycle)),
		CurrentCycleActive:              types.BoolValue(status.CurrentCycleActive),
		CurrentScanMode:                 types.StringValue(status.CurrentScanMode),
		LastCycleEndUnixSecs:            types.Int64Value(int64(status.LastCycleEndUnixSecs)),
		LastCycleResult:                 types.StringValue(status.LastCycleResult),
		LastCycleDurationSeconds:        types.Float64Value(status.LastCycleDurationSeconds),
		LastCycleObjectsScanned:         types.Int64Value(int64(status.LastCycleObjectsScanned)),
		LastCycleDirectoriesScanned:     types.Int64Value(int64(status.LastCycleDirectoriesScanned)),
		CyclesCompleted:                 types.Int64Value(int64(status.CyclesCompleted)),
		FailedCycles:                    types.Int64Value(int64(status.FailedCycles)),
		SupersededCycles:                types.Int64Value(int64(status.SupersededCycles)),
		EffectiveCycleIntervalSeconds:   types.Int64Value(int64(status.EffectiveCycleIntervalSeconds)),
		UsageBootstrapRebuildPending:    types.BoolValue(status.UsageBootstrapRebuildPending),
		UsagePublicationState:           types.StringValue(status.UsagePublicationState),
		UsageLastDurableSuccessUnixSecs: types.Int64Value(int64(status.UsageLastDurableSuccessUnixSecs)),
		UsageDirtyPendingBuckets:        types.Int64Value(int64(status.UsageDirtyPendingBuckets)),
		MaintenancePrimaryControl:       types.StringValue(status.MaintenancePrimaryControl),
		CycleRecoveryState:              types.StringValue(status.CycleRecoveryState),
		CycleRecoveryReason:             types.StringPointerValue(status.CycleRecoveryReason),
		DataMovementPaused:              types.BoolValue(status.DataMovementPaused),
		PauseBacklogPhase:               types.StringValue(status.PauseBacklogPhase),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func init() {
	registerDataSource(NewScannerStatusDataSource)
}
