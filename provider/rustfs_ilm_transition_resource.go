// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &ILMTransitionResource{}
	_ resource.ResourceWithConfigure   = &ILMTransitionResource{}
	_ resource.ResourceWithImportState = &ILMTransitionResource{}
)

func NewILMTransitionResource() resource.Resource {
	return &ILMTransitionResource{}
}

// ILMTransitionResource starts one manual ILM transition run and keeps the
// durable job it returns. It is an action with a state record, not a desired
// state: nothing it does can be reconciled back, and the provider never
// transitions bytes on refresh.
type ILMTransitionResource struct {
	client *AllClient
}

type ILMTransitionResourceModel struct {
	ID                   types.String `tfsdk:"id"`
	Bucket               types.String `tfsdk:"bucket"`
	Prefix               types.String `tfsdk:"prefix"`
	Tier                 types.String `tfsdk:"tier"`
	DryRun               types.Bool   `tfsdk:"dry_run"`
	MaxObjects           types.Int64  `tfsdk:"max_objects"`
	MaxDurationSeconds   types.Int64  `tfsdk:"max_duration_seconds"`
	Triggers             types.Map    `tfsdk:"triggers"`
	JobID                types.String `tfsdk:"job_id"`
	State                types.String `tfsdk:"state"`
	StatusEndpoint       types.String `tfsdk:"status_endpoint"`
	CancelEndpoint       types.String `tfsdk:"cancel_endpoint"`
	CancelRequested      types.Bool   `tfsdk:"cancel_requested"`
	FailureReason        types.String `tfsdk:"failure_reason"`
	CreatedAtUnixNanos   types.Int64  `tfsdk:"created_at_unix_nanos"`
	UpdatedAtUnixNanos   types.Int64  `tfsdk:"updated_at_unix_nanos"`
	CompletedAtUnixNanos types.Int64  `tfsdk:"completed_at_unix_nanos"`
	Report               types.Object `tfsdk:"report"`
	QueueSnapshot        types.Object `tfsdk:"queue_snapshot"`
}

var ilmTransitionReportAttrTypes = map[string]attr.Type{
	"bucket":                       types.StringType,
	"prefix":                       types.StringType,
	"tier":                         types.StringType,
	"dry_run":                      types.BoolType,
	"lifecycle_config_found":       types.BoolType,
	"scanned":                      types.Int64Type,
	"eligible":                     types.Int64Type,
	"enqueued":                     types.Int64Type,
	"dry_run_eligible":             types.Int64Type,
	"skipped_not_transition":       types.Int64Type,
	"skipped_tier":                 types.Int64Type,
	"skipped_delete_marker":        types.Int64Type,
	"skipped_directory":            types.Int64Type,
	"skipped_replication":          types.Int64Type,
	"skipped_already_transitioned": types.Int64Type,
	"skipped_already_in_flight":    types.Int64Type,
	"skipped_queue_full":           types.Int64Type,
	"skipped_queue_closed":         types.Int64Type,
	"skipped_queue_timeout":        types.Int64Type,
	"transition_completed":         types.Int64Type,
	"transition_failed":            types.Int64Type,
	"tier_failure":                 types.Int64Type,
	"tier_failure_by_reason":       types.MapType{ElemType: types.Int64Type},
	"truncated_by_limit":           types.BoolType,
	"truncated_by_duration":        types.BoolType,
	"cancelled":                    types.BoolType,
	"continuation_token":           types.StringType,
}

var ilmTransitionQueueSnapshotAttrTypes = map[string]attr.Type{
	"queue_capacity":       types.Int64Type,
	"queued":               types.Int64Type,
	"active":               types.Int64Type,
	"workers":              types.Int64Type,
	"queue_full":           types.Int64Type,
	"queue_send_timeout":   types.Int64Type,
	"compensation_pending": types.Int64Type,
	"compensation_running": types.Int64Type,
}

func (r *ILMTransitionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ilm_transition"
}

func (r *ILMTransitionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Start a manual ILM transition run",
		MarkdownDescription: "Starts a manual ILM transition run for one bucket (optionally narrowed to a prefix) and " +
			"keeps the durable job the server returns.\n\n" +
			"### This resource moves data\n\n" +
			"A transition copies or moves objects to the named storage tier. A wrong `bucket` or `prefix` can move " +
			"far more data than intended, and moving it back is not a provider operation. `dry_run` reports what " +
			"would be enqueued without moving anything; run it first against an unfamiliar lifecycle configuration.\n\n" +
			"### Lifecycle\n\n" +
			"`Create` starts the run in the server's durable-job mode and records `job_id`; `Read` reports the job's " +
			"current `state` and progress counters. Every configurable attribute carries `RequiresReplace`, so " +
			"changing one starts a **new** run instead of editing the finished one. `triggers` exists only to " +
			"re-run a transition with the same parameters.\n\n" +
			"`Delete` is a **no-op**: the provider never cancels a run, never rolls a transition back and never " +
			"mutates ILM state on destroy. `terraform destroy` only forgets the record. Cancel a run with the admin " +
			"API (`DELETE /rustfs/admin/v3/ilm/transition/jobs/{job_id}`) if that is what you want.\n\n" +
			"### Missing jobs\n\n" +
			"The server answers `NoSuchKey` once it no longer holds the job record; at that point `Read` removes this " +
			"resource from state without error.\n\n" +
			"### Requirements\n\n" +
			"The caller needs the `SetTierAction` admin action for the run. The routes ship from RustFS 1.0.1 on; an " +
			"older server fails with a diagnostic naming the route and its version.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Durable job id of the transition run.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"bucket": schema.StringAttribute{
				Required: true,
				Description: "Bucket whose objects are transitioned. There is no default: a transition must name " +
					"its bucket explicitly. Changing it starts a new run.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"prefix": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(""),
				Description: "Object key prefix to narrow the run. The empty default covers the whole bucket. " +
					"Changing it starts a new run.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tier": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(""),
				Description: "Storage tier to transition to, matched case-insensitively by the server. Empty " +
					"selects the target from the bucket's lifecycle configuration. Changing it starts a new run.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"dry_run": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Report what would be enqueued without moving any object. Changing it starts a new run.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"max_objects": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(0),
				Description: "Maximum number of objects this run scans. `0` uses the server default (10000); the " +
					"server accepts at most 100000. Changing it starts a new run.",
				Validators: []validator.Int64{
					int64validator.Between(0, 100000),
				},
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"max_duration_seconds": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(0),
				Description: "Maximum wall-clock seconds this run may take. `0` leaves the budget to the server; " +
					"the server accepts at most 3600. Changing it starts a new run.",
				Validators: []validator.Int64{
					int64validator.Between(0, 3600),
				},
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"triggers": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Arbitrary values that are not sent to the server. Change one to start a new run with " +
					"the same parameters, for example to re-run a nightly transition.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"job_id": schema.StringAttribute{
				Computed:    true,
				Description: "Durable job id of the transition run; equal to `id`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"state": schema.StringAttribute{
				Computed: true,
				Description: "Job state reported by the server: `accepted` right after the run, then `running`, " +
					"`completed`, `partial`, `failed`, `cancelled` or `unknown`.",
			},
			"status_endpoint": schema.StringAttribute{
				Computed:    true,
				Description: "Admin route that reports this job.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"cancel_endpoint": schema.StringAttribute{
				Computed:    true,
				Description: "Admin route that cancels this job. The provider never calls it.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"cancel_requested": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether a cancellation has been requested on this job out of band.",
			},
			"failure_reason": schema.StringAttribute{
				Computed:    true,
				Description: "Why the job failed, when the server reports one.",
			},
			"created_at_unix_nanos": schema.Int64Attribute{
				Computed:    true,
				Description: "Job creation time in Unix nanoseconds, as reported by the server.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"updated_at_unix_nanos": schema.Int64Attribute{
				Computed:    true,
				Description: "Last job update time in Unix nanoseconds, as reported by the server.",
			},
			"completed_at_unix_nanos": schema.Int64Attribute{
				Computed:    true,
				Description: "Job completion time in Unix nanoseconds; null while the job is still running.",
			},
			"report": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Scan and enqueue counters of the run.",
				Attributes:  ilmTransitionReportSchema(),
			},
			"queue_snapshot": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Transition worker queue pressure recorded with the job.",
				Attributes:  ilmTransitionQueueSnapshotSchema(),
			},
		},
	}
}

func ilmTransitionReportSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"bucket": schema.StringAttribute{
			Computed:    true,
			Description: "Bucket the run covered.",
		},
		"prefix": schema.StringAttribute{
			Computed:    true,
			Description: "Prefix the run covered.",
		},
		"tier": schema.StringAttribute{
			Computed:    true,
			Description: "Target tier, empty when the lifecycle configuration chose it.",
		},
		"dry_run": schema.BoolAttribute{
			Computed:    true,
			Description: "Whether the run was a dry run.",
		},
		"lifecycle_config_found": schema.BoolAttribute{
			Computed:    true,
			Description: "Whether a lifecycle configuration was found for the bucket.",
		},
		"scanned": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects scanned.",
		},
		"eligible": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects eligible for transition.",
		},
		"enqueued": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects enqueued for transition.",
		},
		"dry_run_eligible": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects a dry run found eligible.",
		},
		"skipped_not_transition": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects skipped because no transition applied.",
		},
		"skipped_tier": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects skipped because the target tier did not match.",
		},
		"skipped_delete_marker": schema.Int64Attribute{
			Computed:    true,
			Description: "Delete markers skipped.",
		},
		"skipped_directory": schema.Int64Attribute{
			Computed:    true,
			Description: "Directory entries skipped.",
		},
		"skipped_replication": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects skipped because replication owns them.",
		},
		"skipped_already_transitioned": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects already transitioned.",
		},
		"skipped_already_in_flight": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects already being transitioned.",
		},
		"skipped_queue_full": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects skipped because the transition queue was full.",
		},
		"skipped_queue_closed": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects skipped because the transition queue was closed.",
		},
		"skipped_queue_timeout": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects skipped because enqueueing timed out.",
		},
		"transition_completed": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects whose transition completed.",
		},
		"transition_failed": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects whose transition failed.",
		},
		"tier_failure": schema.Int64Attribute{
			Computed:    true,
			Description: "Tier-level failures.",
		},
		"tier_failure_by_reason": schema.MapAttribute{
			Computed:    true,
			ElementType: types.Int64Type,
			Description: "Tier failures grouped by reason.",
		},
		"truncated_by_limit": schema.BoolAttribute{
			Computed:    true,
			Description: "Whether the object budget truncated the run.",
		},
		"truncated_by_duration": schema.BoolAttribute{
			Computed:    true,
			Description: "Whether the duration budget truncated the run.",
		},
		"cancelled": schema.BoolAttribute{
			Computed:    true,
			Description: "Whether the run was cancelled.",
		},
		"continuation_token": schema.StringAttribute{
			Computed:    true,
			Description: "Token for a follow-up run when the scan was truncated.",
		},
	}
}

func ilmTransitionQueueSnapshotSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"queue_capacity": schema.Int64Attribute{
			Computed:    true,
			Description: "Transition queue capacity.",
		},
		"queued": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects waiting in the transition queue.",
		},
		"active": schema.Int64Attribute{
			Computed:    true,
			Description: "Objects actively transitioning.",
		},
		"workers": schema.Int64Attribute{
			Computed:    true,
			Description: "Transition workers.",
		},
		"queue_full": schema.Int64Attribute{
			Computed:    true,
			Description: "Enqueue attempts refused because the queue was full.",
		},
		"queue_send_timeout": schema.Int64Attribute{
			Computed:    true,
			Description: "Enqueue attempts that timed out.",
		},
		"compensation_pending": schema.Int64Attribute{
			Computed:    true,
			Description: "Compensations waiting to run.",
		},
		"compensation_running": schema.Int64Attribute{
			Computed:    true,
			Description: "Compensations running.",
		},
	}
}

func (r *ILMTransitionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	r.client = client
}

func (r *ILMTransitionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ILMTransitionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	run, err := r.client.RustClient.RunILMTransition(ctx, rustfs.ILMTransitionRunOptions{
		Bucket:             plan.Bucket.ValueString(),
		Prefix:             plan.Prefix.ValueString(),
		Tier:               plan.Tier.ValueString(),
		DryRun:             plan.DryRun.ValueBool(),
		MaxObjects:         plan.MaxObjects.ValueInt64(),
		MaxDurationSeconds: plan.MaxDurationSeconds.ValueInt64(),
		Async:              true,
	})
	if err != nil {
		addILMTransitionRunError(&resp.Diagnostics, plan, err)
		return
	}
	if run.JobID == "" {
		resp.Diagnostics.AddError(
			"Error starting ILM transition",
			"RustFS accepted the transition run without a job id, so no state can be tracked. "+
				"The run may still be active; inspect it with POST /rustfs/admin/v3/ilm/transition/run "+
				"or the job routes before retrying.",
		)
		return
	}

	plan.ID = types.StringValue(run.JobID)
	plan.JobID = types.StringValue(run.JobID)
	plan.State = types.StringValue(run.State)
	plan.StatusEndpoint = types.StringValue(run.StatusEndpoint)
	plan.CancelEndpoint = types.StringValue(run.CancelEndpoint)
	plan.CancelRequested = types.BoolValue(false)
	plan.FailureReason = types.StringNull()
	plan.CreatedAtUnixNanos = types.Int64Null()
	plan.UpdatedAtUnixNanos = types.Int64Null()
	plan.CompletedAtUnixNanos = types.Int64Null()
	plan.Report = ilmTransitionReportValue(run.Report)
	plan.QueueSnapshot = types.ObjectNull(ilmTransitionQueueSnapshotAttrTypes)

	tflog.Trace(ctx, "started an ILM transition resource", map[string]any{"job_id": run.JobID})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ILMTransitionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ILMTransitionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobID := state.JobID.ValueString()
	if jobID == "" {
		jobID = state.ID.ValueString()
	}
	if jobID == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	job, err := r.client.RustClient.ILMTransitionJob(ctx, jobID)
	if err != nil {
		// The server reports a job it does not hold as NoSuchKey. A missing
		// route is a different failure and must not silently drop the record.
		if rustfs.IsAPIErrorCode(err, "NoSuchKey") {
			tflog.Debug(ctx, "ILM transition job is gone; removing it from state", map[string]any{"job_id": jobID})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading ILM transition job",
			"Could not read job "+jobID+": "+err.Error(),
		)
		return
	}

	state.ID = types.StringValue(job.JobID)
	state.JobID = types.StringValue(job.JobID)
	state.State = types.StringValue(job.Status)
	state.StatusEndpoint = types.StringValue(job.StatusEndpoint)
	state.CancelEndpoint = types.StringValue(job.CancelEndpoint)
	state.CancelRequested = types.BoolValue(job.CancelRequested)
	if job.FailureReason == "" {
		state.FailureReason = types.StringNull()
	} else {
		state.FailureReason = types.StringValue(job.FailureReason)
	}
	state.CreatedAtUnixNanos = types.Int64Value(job.CreatedAtUnixNanos)
	state.UpdatedAtUnixNanos = types.Int64Value(job.UpdatedAtUnixNanos)
	if job.CompletedAtUnixNanos == nil {
		state.CompletedAtUnixNanos = types.Int64Null()
	} else {
		state.CompletedAtUnixNanos = types.Int64Value(*job.CompletedAtUnixNanos)
	}
	state.Report = ilmTransitionReportValue(job.Report)
	state.QueueSnapshot = ilmTransitionQueueSnapshotValue(job.QueueSnapshot)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ILMTransitionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every configurable attribute carries RequiresReplace, so Terraform never
	// calls Update for a real change. Copy the plan through so a framework
	// caller that does call it still gets a consistent state.
	var plan ILMTransitionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ILMTransitionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Deliberately a no-op. Cancelling would stop remaining work, not undo the
	// bytes already transitioned, and destroying infrastructure must not
	// mutate ILM state.
	var state ILMTransitionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Trace(ctx, "deleted an ILM transition resource (no-op)", map[string]any{"job_id": state.JobID.ValueString()})
}

func (r *ILMTransitionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func addILMTransitionRunError(diags *diag.Diagnostics, plan ILMTransitionResourceModel, err error) {
	if rustfs.IsUnsupportedFeature(err) {
		diags.AddError(
			"ILM transition is not available on this RustFS server",
			err.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
				"rustfs_ilm_transition resource from the configuration.",
		)
		return
	}
	diags.AddError(
		"Error starting ILM transition",
		"Could not start a transition run for bucket "+plan.Bucket.ValueString()+
			" with prefix "+plan.Prefix.ValueString()+": "+err.Error(),
	)
}

func ilmTransitionReportValue(report rustfs.ILMTransitionReport) types.Object {
	reasons := make(map[string]attr.Value, len(report.TierFailureByReason))
	for reason, count := range report.TierFailureByReason {
		reasons[reason] = types.Int64Value(count)
	}
	return types.ObjectValueMust(ilmTransitionReportAttrTypes, map[string]attr.Value{
		"bucket":                       types.StringValue(report.Bucket),
		"prefix":                       types.StringValue(report.Prefix),
		"tier":                         types.StringValue(report.Tier),
		"dry_run":                      types.BoolValue(report.DryRun),
		"lifecycle_config_found":       types.BoolValue(report.LifecycleConfigFound),
		"scanned":                      types.Int64Value(report.Scanned),
		"eligible":                     types.Int64Value(report.Eligible),
		"enqueued":                     types.Int64Value(report.Enqueued),
		"dry_run_eligible":             types.Int64Value(report.DryRunEligible),
		"skipped_not_transition":       types.Int64Value(report.SkippedNotTransition),
		"skipped_tier":                 types.Int64Value(report.SkippedTier),
		"skipped_delete_marker":        types.Int64Value(report.SkippedDeleteMarker),
		"skipped_directory":            types.Int64Value(report.SkippedDirectory),
		"skipped_replication":          types.Int64Value(report.SkippedReplication),
		"skipped_already_transitioned": types.Int64Value(report.SkippedAlreadyTransitioned),
		"skipped_already_in_flight":    types.Int64Value(report.SkippedAlreadyInFlight),
		"skipped_queue_full":           types.Int64Value(report.SkippedQueueFull),
		"skipped_queue_closed":         types.Int64Value(report.SkippedQueueClosed),
		"skipped_queue_timeout":        types.Int64Value(report.SkippedQueueTimeout),
		"transition_completed":         types.Int64Value(report.TransitionCompleted),
		"transition_failed":            types.Int64Value(report.TransitionFailed),
		"tier_failure":                 types.Int64Value(report.TierFailure),
		"tier_failure_by_reason":       types.MapValueMust(types.Int64Type, reasons),
		"truncated_by_limit":           types.BoolValue(report.TruncatedByLimit),
		"truncated_by_duration":        types.BoolValue(report.TruncatedByDuration),
		"cancelled":                    types.BoolValue(report.Cancelled),
		"continuation_token":           types.StringValue(report.ContinuationToken),
	})
}

func ilmTransitionQueueSnapshotValue(snapshot rustfs.ILMTransitionQueueSnapshot) types.Object {
	return types.ObjectValueMust(ilmTransitionQueueSnapshotAttrTypes, map[string]attr.Value{
		"queue_capacity":       types.Int64Value(snapshot.QueueCapacity),
		"queued":               types.Int64Value(snapshot.Queued),
		"active":               types.Int64Value(snapshot.Active),
		"workers":              types.Int64Value(snapshot.Workers),
		"queue_full":           types.Int64Value(snapshot.QueueFull),
		"queue_send_timeout":   types.Int64Value(snapshot.QueueSendTimeout),
		"compensation_pending": types.Int64Value(snapshot.CompensationPending),
		"compensation_running": types.Int64Value(snapshot.CompensationRunning),
	})
}

func init() {
	registerResource(NewILMTransitionResource)
}
