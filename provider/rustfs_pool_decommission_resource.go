// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                   = &PoolDecommissionResource{}
	_ resource.ResourceWithConfigure      = &PoolDecommissionResource{}
	_ resource.ResourceWithImportState    = &PoolDecommissionResource{}
	_ resource.ResourceWithValidateConfig = &PoolDecommissionResource{}
)

// PoolDecommissionResource drains and retires one RustFS storage pool.
//
// Create starts the drain, Read mirrors decommission/status, and Delete cancels
// an in-flight drain and clears the metadata left by a canceled or failed one.
// A completed drain is terminal and is left untouched on destroy.
type PoolDecommissionResource struct {
	client *AllClient
}

type PoolDecommissionResourceModel struct {
	ID                          types.String `tfsdk:"id"`
	Pool                        types.String `tfsdk:"pool"`
	Confirm                     types.String `tfsdk:"confirm"`
	Status                      types.String `tfsdk:"status"`
	PoolStatus                  types.String `tfsdk:"pool_status"`
	StartedAt                   types.String `tfsdk:"started_at"`
	Complete                    types.Bool   `tfsdk:"complete"`
	Failed                      types.Bool   `tfsdk:"failed"`
	Canceled                    types.Bool   `tfsdk:"canceled"`
	Queued                      types.Bool   `tfsdk:"queued"`
	QueuedBuckets               types.List   `tfsdk:"queued_buckets"`
	DecommissionedBuckets       types.List   `tfsdk:"decommissioned_buckets"`
	Bucket                      types.String `tfsdk:"bucket"`
	Prefix                      types.String `tfsdk:"prefix"`
	Object                      types.String `tfsdk:"object"`
	Stage                       types.String `tfsdk:"stage"`
	WaitingReason               types.String `tfsdk:"waiting_reason"`
	StartSize                   types.Int64  `tfsdk:"start_size"`
	TotalSize                   types.Int64  `tfsdk:"total_size"`
	CurrentSize                 types.Int64  `tfsdk:"current_size"`
	ObjectsDecommissioned       types.Int64  `tfsdk:"objects_decommissioned"`
	ObjectsDecommissionedFailed types.Int64  `tfsdk:"objects_decommissioned_failed"`
	BytesDecommissioned         types.Int64  `tfsdk:"bytes_decommissioned"`
	BytesDecommissionedFailed   types.Int64  `tfsdk:"bytes_decommissioned_failed"`
}

func NewPoolDecommissionResource() resource.Resource {
	return &PoolDecommissionResource{}
}

func (r *PoolDecommissionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pool_decommission"
}

func (r *PoolDecommissionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Drain and retire a RustFS storage pool",
		MarkdownDescription: "Drain and retire a RustFS storage pool.\n\n" +
			"**This resource moves data.** Creating it starts a decommission: RustFS copies every " +
			"object of the named pool to the remaining pools and then retires the pool. Retiring the " +
			"pool from the deployment still requires removing its volume expression from the server " +
			"configuration and restarting RustFS; this resource only drives the drain and reports its " +
			"progress.\n\n" +
			"The deployment must have at least two storage pools; RustFS answers `501 NotImplemented` " +
			"on a single-pool deployment. The drain needs enough free space on the remaining pools for " +
			"the drained data plus overhead, and a decommission cannot run while a rebalance is in " +
			"progress.\n\n" +
			"Destroying an in-flight drain cancels it and clears the canceled record. Canceling does " +
			"not move already relocated objects back, and a completed drain cannot be canceled or " +
			"cleared: destroy leaves the completed record in place.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Terraform resource ID; the pool command line.",
			},
			"pool": schema.StringAttribute{
				Required: true,
				Description: "Command line of the storage pool to drain, exactly as `data.rustfs_pools.names` " +
					"reports it (for example `/data{1...4}`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"confirm": schema.StringAttribute{
				Required: true,
				Description: "Safety interlock: must repeat `pool` exactly. Starting a decommission moves data off " +
					"the pool, so the target has to be named twice.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Decommission status reported by the server: `none`, `running`, `complete`, `failed` or `canceled`.",
			},
			"pool_status": schema.StringAttribute{
				Computed:    true,
				Description: "Pool lifecycle status reported alongside the drain, for example `active`, `decommissioning`, `decommissioned` or `blocked`.",
			},
			"started_at": schema.StringAttribute{
				Computed:    true,
				Description: "Start time of the drain as reported by the server, in RFC 3339. Null after a canceled drain.",
			},
			"complete": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the drain finished successfully.",
			},
			"failed": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the drain failed.",
			},
			"canceled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the drain was canceled.",
			},
			"queued": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the drain is queued behind another pool operation.",
			},
			"queued_buckets": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Buckets still waiting to be drained.",
			},
			"decommissioned_buckets": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Buckets already drained from the pool.",
			},
			"bucket": schema.StringAttribute{
				Computed:    true,
				Description: "Bucket the drain is currently working on.",
			},
			"prefix": schema.StringAttribute{
				Computed:    true,
				Description: "Object prefix the drain is currently working on.",
			},
			"object": schema.StringAttribute{
				Computed:    true,
				Description: "Object the drain is currently moving.",
			},
			"stage": schema.StringAttribute{
				Computed:    true,
				Description: "Current drain stage reported by the server.",
			},
			"waiting_reason": schema.StringAttribute{
				Computed:    true,
				Description: "Why the drain is waiting, when the server reports a reason.",
			},
			"start_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Free bytes on the pool when the drain started.",
			},
			"total_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Total size of the pool in bytes.",
			},
			"current_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Current free bytes on the pool.",
			},
			"objects_decommissioned": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of objects drained successfully.",
			},
			"objects_decommissioned_failed": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of objects that failed to drain.",
			},
			"bytes_decommissioned": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of bytes drained successfully.",
			},
			"bytes_decommissioned_failed": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of bytes that failed to drain.",
			},
		},
	}
}

func (r *PoolDecommissionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the confirmation interlock the schema cannot express.
func (r *PoolDecommissionResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config PoolDecommissionResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Pool.IsNull() || config.Pool.IsUnknown() || config.Confirm.IsNull() || config.Confirm.IsUnknown() {
		return
	}
	if config.Pool.ValueString() != config.Confirm.ValueString() {
		resp.Diagnostics.AddAttributeError(
			path.Root("confirm"),
			"Invalid pool decommission confirmation",
			"confirm must repeat pool exactly ("+config.Pool.ValueString()+"); a decommission moves data off the "+
				"pool and must be deliberate.",
		)
	}
}

func (r *PoolDecommissionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PoolDecommissionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pool := plan.Pool.ValueString()
	if plan.Confirm.ValueString() != pool {
		resp.Diagnostics.AddError(
			"Invalid pool decommission confirmation",
			"confirm must repeat pool exactly ("+pool+").",
		)
		return
	}

	statuses, err := r.client.RustClient.DecommissionStatuses(ctx)
	if err != nil {
		r.addDecommissionError(ctx, &resp.Diagnostics, "Error starting pool decommission", "start a decommission of", pool, err)
		return
	}

	current := findPoolDecommission(statuses, pool)
	if current == nil {
		resp.Diagnostics.AddError(
			"Error starting pool decommission",
			"The deployment does not report a storage pool named "+fmt.Sprintf("%q", pool)+". Current pools: "+
				quotedPoolNames(statuses)+".",
		)
		return
	}

	switch current.Status {
	case "none":
		if err := r.client.RustClient.StartPoolDecommission(ctx, pool); err != nil {
			r.addDecommissionError(ctx, &resp.Diagnostics, "Error starting pool decommission", "start a decommission of", pool, err)
			return
		}
		// Re-read instead of assuming the accepted request is already visible
		// as running: the drain may complete before the response is handled.
		statuses, err = r.client.RustClient.DecommissionStatuses(ctx)
		if err != nil {
			r.addDecommissionError(ctx, &resp.Diagnostics, "Error reading pool decommission", "read the decommission of", pool, err)
			return
		}
		current = findPoolDecommission(statuses, pool)
		if current == nil || current.Status == "none" {
			resp.Diagnostics.AddError(
				"Error reading pool decommission",
				"The drain of pool "+fmt.Sprintf("%q", pool)+" was accepted, but the server does not report a "+
					"decommission record for it. Refresh to observe the drain.",
			)
			return
		}
		tflog.Trace(ctx, "started a pool decommission", map[string]any{"pool": pool})
	case "running", "complete":
		// The requested end state already exists. Adopt it so a retried apply
		// after a lost response does not fail, and so importing a drained pool
		// records its progress.
		tflog.Trace(ctx, "adopted an existing pool decommission", map[string]any{"pool": pool, "status": current.Status})
	case "failed", "canceled":
		resp.Diagnostics.AddError(
			"Error starting pool decommission",
			"Pool "+fmt.Sprintf("%q", pool)+" already carries a "+current.Status+" decommission record. Clear it on "+
				"the server before Terraform starts a new drain.",
		)
		return
	default:
		resp.Diagnostics.AddError(
			"Error starting pool decommission",
			"Pool "+fmt.Sprintf("%q", pool)+" reports the unknown decommission status "+fmt.Sprintf("%q", current.Status)+".",
		)
		return
	}

	resp.Diagnostics.Append(applyPoolDecommission(ctx, &plan, *current)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PoolDecommissionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PoolDecommissionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pool := state.Pool.ValueString()
	statuses, err := r.client.RustClient.DecommissionStatuses(ctx)
	if err != nil {
		r.addDecommissionError(ctx, &resp.Diagnostics, "Error reading pool decommission", "read the decommission of", pool, err)
		return
	}

	current := findPoolDecommission(statuses, pool)
	if current == nil || current.Status == "none" {
		// The pool is gone from the deployment, or its decommission record was
		// cleared behind Terraform's back. Either way there is nothing left to
		// manage, and calling a mutation route here would be unsafe.
		tflog.Trace(ctx, "pool decommission no longer exists", map[string]any{"pool": pool})
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(applyPoolDecommission(ctx, &state, *current)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PoolDecommissionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PoolDecommissionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Every configurable attribute is RequiresReplace, so Terraform plans a
	// replacement instead of calling this method. Writing the plan back keeps
	// the state consistent if the framework invokes it anyway.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PoolDecommissionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PoolDecommissionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pool := state.Pool.ValueString()
	statuses, err := r.client.RustClient.DecommissionStatuses(ctx)
	if err != nil {
		r.addDecommissionError(ctx, &resp.Diagnostics, "Error canceling pool decommission", "cancel the decommission of", pool, err)
		return
	}

	current := findPoolDecommission(statuses, pool)
	if current == nil || current.Status == "none" {
		// No decommission record: destroy is already complete.
		return
	}

	switch current.Status {
	case "complete":
		// A completed drain is terminal; RustFS rejects both cancel and clear
		// for it. Leave the record in place so a refresh still reports success.
		tflog.Trace(ctx, "pool decommission is complete; nothing to cancel", map[string]any{"pool": pool})
		return
	case "failed", "canceled":
		r.clearPoolDecommission(ctx, pool, &resp.Diagnostics)
		return
	case "running", "queued":
		if cancelErr := r.client.RustClient.CancelPoolDecommission(ctx, pool); cancelErr != nil {
			// RustFS answers 400 InvalidRequest when the drain finished between
			// the status read and the cancel. Re-read once before failing.
			if !rustfs.IsAPIErrorCode(cancelErr, "InvalidRequest") {
				r.addDecommissionError(ctx, &resp.Diagnostics, "Error canceling pool decommission", "cancel the decommission of", pool, cancelErr)
				return
			}
			var readErr error
			statuses, readErr = r.client.RustClient.DecommissionStatuses(ctx)
			if readErr != nil {
				r.addDecommissionError(ctx, &resp.Diagnostics, "Error canceling pool decommission", "cancel the decommission of", pool, readErr)
				return
			}
			current = findPoolDecommission(statuses, pool)
			if current == nil || current.Status == "none" || current.Status == "complete" {
				return
			}
			if current.Status == "failed" || current.Status == "canceled" {
				r.clearPoolDecommission(ctx, pool, &resp.Diagnostics)
				return
			}
			r.addDecommissionError(ctx, &resp.Diagnostics, "Error canceling pool decommission", "cancel the decommission of", pool, cancelErr)
			return
		}
		tflog.Trace(ctx, "canceled a pool decommission", map[string]any{"pool": pool})

		// Clear the canceled record so a later apply can decommission the pool
		// again. A drain that completed in the meantime must not be cleared.
		statuses, err = r.client.RustClient.DecommissionStatuses(ctx)
		if err != nil {
			r.addDecommissionError(ctx, &resp.Diagnostics, "Error clearing pool decommission", "clear the decommission of", pool, err)
			return
		}
		current = findPoolDecommission(statuses, pool)
		if current == nil || current.Status == "none" || current.Status == "complete" {
			return
		}
		if current.Status == "failed" || current.Status == "canceled" {
			r.clearPoolDecommission(ctx, pool, &resp.Diagnostics)
			return
		}
		resp.Diagnostics.AddError(
			"Error canceling pool decommission",
			"The cancel request for pool "+fmt.Sprintf("%q", pool)+" was accepted, but the server still reports "+
				fmt.Sprintf("%q", current.Status)+".",
		)
	default:
		resp.Diagnostics.AddError(
			"Error canceling pool decommission",
			"Pool "+fmt.Sprintf("%q", pool)+" reports the unknown decommission status "+fmt.Sprintf("%q", current.Status)+".",
		)
	}
}

func (r *PoolDecommissionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The pool command line is the import ID. Read fills the computed state.
	resource.ImportStatePassthroughID(ctx, path.Root("pool"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// clearPoolDecommission clears a canceled or failed decommission record. A
// clear that races a completion is treated as success: there is then nothing
// left for Terraform to cancel.
func (r *PoolDecommissionResource) clearPoolDecommission(ctx context.Context, pool string, diags *diag.Diagnostics) {
	if err := r.client.RustClient.ClearPoolDecommission(ctx, pool); err != nil {
		statuses, readErr := r.client.RustClient.DecommissionStatuses(ctx)
		if readErr == nil {
			current := findPoolDecommission(statuses, pool)
			if current == nil || current.Status == "none" || current.Status == "complete" {
				return
			}
		}
		r.addDecommissionError(ctx, diags, "Error clearing pool decommission", "clear the decommission of", pool, err)
		return
	}
	tflog.Trace(ctx, "cleared a pool decommission", map[string]any{"pool": pool})
}

// addDecommissionError reports err, replacing the server text with a topology
// explanation when the 501 comes from a single-pool deployment.
func (r *PoolDecommissionResource) addDecommissionError(ctx context.Context, diags *diag.Diagnostics, summary, action, pool string, err error) {
	if err == nil {
		err = errors.New("the server returned no error detail")
	}
	detail := "Could not " + action + " pool " + fmt.Sprintf("%q", pool) + ": " + err.Error()
	if explanation, ok := r.singlePoolExplanation(ctx, err); ok {
		detail = "Could not " + action + " pool " + fmt.Sprintf("%q", pool) + ": " + explanation
	}
	diags.AddError(summary, detail)
}

// singlePoolExplanation recognises the 501 RustFS answers for every
// decommission route on a single-pool deployment and names the current pools.
func (r *PoolDecommissionResource) singlePoolExplanation(ctx context.Context, err error) (string, bool) {
	if !poolDecommissionUnsupported(err) {
		return "", false
	}
	pools, listErr := r.client.RustClient.ListPools(ctx)
	// Only an exactly-one-pool answer proves the topology gap. An empty list
	// means the pool inventory could not be determined, so the raw 501 is the
	// truthful diagnosis.
	if listErr != nil || len(pools) != 1 {
		return "", false
	}
	names := make([]string, 0, len(pools))
	for _, pool := range pools {
		names = append(names, fmt.Sprintf("%q", pool.Name))
	}
	return "RustFS serves the decommission routes only on a deployment with at least two storage pools. " +
		"This deployment reports " + quotedNames(names) + "; add a second pool before decommissioning one.", true
}

func poolDecommissionUnsupported(err error) bool {
	if rustfs.IsUnsupportedFeature(err) {
		return true
	}
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotImplemented || strings.EqualFold(apiErr.Code, "NotImplemented")
}

func findPoolDecommission(statuses []rustfs.PoolDecommissionStatus, pool string) *rustfs.PoolDecommissionStatus {
	for i := range statuses {
		if statuses[i].Cmdline == pool {
			return &statuses[i]
		}
	}
	return nil
}

func quotedPoolNames(statuses []rustfs.PoolDecommissionStatus) string {
	names := make([]string, 0, len(statuses))
	for _, status := range statuses {
		names = append(names, fmt.Sprintf("%q", status.Cmdline))
	}
	return quotedNames(names)
}

func quotedNames(quoted []string) string {
	if len(quoted) == 0 {
		return "no pools"
	}
	return strings.Join(quoted, ", ")
}

// applyPoolDecommission copies the server-reported drain state into model and
// sets every computed attribute, so a plan never sees unknown leftovers.
func applyPoolDecommission(ctx context.Context, model *PoolDecommissionResourceModel, status rustfs.PoolDecommissionStatus) diag.Diagnostics {
	model.ID = types.StringValue(status.Cmdline)
	model.Pool = types.StringValue(status.Cmdline)
	// The confirmation is configuration, not server state. Import has no
	// configuration, so mirroring the pool keeps state self-consistent and
	// ImportStateVerify green; ValidateConfig still rejects a mismatch.
	model.Confirm = types.StringValue(status.Cmdline)
	model.Status = types.StringValue(status.Status)
	model.PoolStatus = types.StringValue(status.PoolStatus)

	info := rustfs.PoolDecommissionInfo{}
	if status.Info != nil {
		info = *status.Info
	}

	model.StartedAt = types.StringPointerValue(info.StartTime)
	model.Complete = types.BoolValue(info.Complete)
	model.Failed = types.BoolValue(info.Failed)
	model.Canceled = types.BoolValue(info.Canceled)
	model.Queued = types.BoolValue(info.Queued)
	model.Bucket = types.StringValue(info.Bucket)
	model.Prefix = types.StringValue(info.Prefix)
	model.Object = types.StringValue(info.Object)
	model.Stage = types.StringValue(info.Stage)
	model.WaitingReason = types.StringPointerValue(info.WaitingReason)
	model.StartSize = types.Int64Value(info.StartSize)
	model.TotalSize = types.Int64Value(info.TotalSize)
	model.CurrentSize = types.Int64Value(info.CurrentSize)
	model.ObjectsDecommissioned = types.Int64Value(info.ObjectsDecommissioned)
	model.ObjectsDecommissionedFailed = types.Int64Value(info.ObjectsDecommissionedFailed)
	model.BytesDecommissioned = types.Int64Value(info.BytesDecommissioned)
	model.BytesDecommissionedFailed = types.Int64Value(info.BytesDecommissionedFailed)

	var diags diag.Diagnostics
	queued, listDiags := types.ListValueFrom(ctx, types.StringType, info.QueuedBuckets)
	diags.Append(listDiags...)
	model.QueuedBuckets = queued

	decommissioned, listDiags := types.ListValueFrom(ctx, types.StringType, info.DecommissionedBuckets)
	diags.Append(listDiags...)
	model.DecommissionedBuckets = decommissioned

	return diags
}

func init() {
	registerResource(NewPoolDecommissionResource)
}
