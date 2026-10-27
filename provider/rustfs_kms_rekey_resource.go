// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &kmsRekeyResource{}
	_ resource.ResourceWithImportState = &kmsRekeyResource{}
)

// NewKmsRekeyResource starts and observes a RustFS bulk rekey sweep.
func NewKmsRekeyResource() resource.Resource {
	return &kmsRekeyResource{}
}

type kmsRekeyResource struct {
	client *AllClient
}

type kmsRekeyResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Buckets        types.List   `tfsdk:"buckets"`
	Prefix         types.String `tfsdk:"prefix"`
	JobID          types.String `tfsdk:"job_id"`
	State          types.String `tfsdk:"state"`
	CurrentBucket  types.String `tfsdk:"current_bucket"`
	Scanned        types.Int64  `tfsdk:"scanned"`
	Rewrapped      types.Int64  `tfsdk:"rewrapped"`
	AlreadyCurrent types.Int64  `tfsdk:"already_current"`
	NotApplicable  types.Int64  `tfsdk:"not_applicable"`
	Failed         types.Int64  `tfsdk:"failed"`
}

func (r *kmsRekeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kms_rekey"
}

func (r *kmsRekeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Start and observe a RustFS KMS bulk rekey sweep",
		MarkdownDescription: "Start a RustFS bulk rekey sweep over one or more buckets or prefixes.\n\n" +
			"Rekey re-wraps the stored data-key envelopes under the current key-encryption key. It " +
			"does not rewrite object bodies, does not change ETags or object versions, and never " +
			"destroys a superseded key version: objects that were already re-wrapped stay readable " +
			"while the sweep runs and after it is cancelled.\n\n" +
			"Creating this resource starts the sweep and returns once the server accepts it. " +
			"Refreshing reads the current or most recent sweep, so `state`, `current_bucket` and the " +
			"counters update while it runs. Destroying a running sweep requests cancellation; the " +
			"cancel is cooperative and does not move already re-wrapped envelopes back. A completed " +
			"sweep is left alone.\n\n" +
			"RustFS answers `501` when the configured KMS backend cannot re-wrap envelopes: Local, " +
			"Static and AWS cannot, while Vault KV2 and Vault Transit can. A second sweep is refused " +
			"while one is running.\n\n" +
			"Import takes the server's job id. The configured `buckets` and `prefix` are not " +
			"recoverable from the server, so set them in configuration after importing.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Terraform resource ID; the server's job id.",
			},
			"buckets": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Buckets to sweep. Omit or leave empty to sweep every bucket. Changing this starts a new sweep.",
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
			},
			"prefix": schema.StringAttribute{
				Optional:    true,
				Description: "Object-key prefix to restrict the sweep to. Changing this starts a new sweep.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"job_id": schema.StringAttribute{
				Computed:    true,
				Description: "Server-generated id of the sweep.",
			},
			"state": schema.StringAttribute{
				Computed:    true,
				Description: "Sweep state: `running`, `completed` or `cancelled`.",
			},
			"current_bucket": schema.StringAttribute{
				Computed:    true,
				Description: "Bucket currently being walked, empty when the sweep is between buckets or finished.",
			},
			"scanned": schema.Int64Attribute{
				Computed:    true,
				Description: "Object versions the walk has yielded.",
			},
			"rewrapped": schema.Int64Attribute{
				Computed:    true,
				Description: "Envelopes re-wrapped and persisted.",
			},
			"already_current": schema.Int64Attribute{
				Computed:    true,
				Description: "Envelopes already on the current key version.",
			},
			"not_applicable": schema.Int64Attribute{
				Computed:    true,
				Description: "Versions with nothing to re-wrap (plaintext, SSE-C or MinIO-sealed).",
			},
			"failed": schema.Int64Attribute{
				Computed:    true,
				Description: "Versions whose re-wrap or metadata write failed. A re-run retries them.",
			},
		},
	}
}

func (r *kmsRekeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *kmsRekeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kmsRekeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var buckets []string
	if !plan.Buckets.IsNull() && !plan.Buckets.IsUnknown() {
		resp.Diagnostics.Append(plan.Buckets.ElementsAs(ctx, &buckets, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	status, err := r.client.RustClient.StartKmsRekey(ctx, buckets, plan.Prefix.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error starting KMS rekey",
			r.startFailureDetail(ctx, err),
		)
		return
	}

	applyKmsRekeyStatus(&plan, status)
	tflog.Trace(ctx, "started a KMS rekey sweep", map[string]any{"job_id": status.JobID})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *kmsRekeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kmsRekeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := r.client.RustClient.GetKmsRekeyStatus(ctx)
	if err != nil {
		if errors.Is(err, rustfs.ErrKmsRekeyNotRun) {
			tflog.Trace(ctx, "KMS rekey sweep no longer exists")
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading KMS rekey",
			"Could not read KMS rekey status: "+err.Error(),
		)
		return
	}
	// An imported or older state may carry no job id yet; adopt the server's
	// id in that case. The server keeps one record, so a different non-empty id
	// means this resource no longer describes the sweep the server reports.
	if state.JobID.ValueString() != "" && status.JobID != state.JobID.ValueString() {
		tflog.Trace(ctx, "KMS rekey sweep was replaced by another job", map[string]any{"job_id": status.JobID})
		resp.State.RemoveResource(ctx)
		return
	}

	applyKmsRekeyStatus(&state, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kmsRekeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan kmsRekeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Every configurable attribute is RequiresReplace, so Terraform plans a
	// replacement instead of calling this method.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *kmsRekeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state kmsRekeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := r.client.RustClient.GetKmsRekeyStatus(ctx)
	if err != nil {
		if errors.Is(err, rustfs.ErrKmsRekeyNotRun) {
			return
		}
		resp.Diagnostics.AddError(
			"Error canceling KMS rekey",
			"Could not read KMS rekey status before canceling: "+err.Error(),
		)
		return
	}
	if status.JobID != state.JobID.ValueString() || status.State != "running" {
		tflog.Trace(ctx, "KMS rekey sweep has nothing to cancel", map[string]any{"job_id": status.JobID, "state": status.State})
		return
	}

	if _, err := r.client.RustClient.CancelKmsRekey(ctx); err != nil {
		if errors.Is(err, rustfs.ErrKmsRekeyNotRun) {
			return
		}
		resp.Diagnostics.AddError(
			"Error canceling KMS rekey",
			"Could not cancel KMS rekey sweep: "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "canceled a KMS rekey sweep", map[string]any{"job_id": status.JobID})
}

func (r *kmsRekeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	if resp.Diagnostics.HasError() {
		return
	}
	// Import takes the server's job id as the state id; Read needs job_id to
	// tell "this sweep" from a later one.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("job_id"), req.ID)...)
}

// kmsRekeyRoute is the start route named in a capability diagnostic.
const kmsRekeyRoute = "POST /rustfs/admin/v3/kms/keys/rekey"

// startFailureDetail explains a refused start. A 501 is not a missing route:
// the route exists and the configured KMS backend cannot re-wrap envelopes, so
// the detail names the route, the backend and the server version instead of
// echoing the raw capability answer. Every other error keeps the server text.
func (r *kmsRekeyResource) startFailureDetail(ctx context.Context, err error) string {
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotImplemented {
		return "Could not start KMS rekey sweep: " + err.Error()
	}

	backend := "the configured KMS backend"
	if config, configErr := r.client.RustClient.GetKmsConfig(ctx); configErr == nil && config.Backend != "" {
		backend = "the " + config.Backend + " KMS backend"
	}

	detail := "Could not start KMS rekey sweep: " + kmsRekeyRoute + " is served, but " + backend +
		" does not support rewrapping data-key envelopes. Local, Static and AWS cannot; " +
		"Vault KV2 and Vault Transit can."
	version, raw, versionErr := r.client.RustClient.ServerVersion(ctx)
	switch {
	case versionErr == nil:
		detail += " RustFS server version: " + version.String() + "."
	case raw != "":
		detail += " RustFS server version: " + raw + "."
	}
	return detail
}

// applyKmsRekeyStatus copies server progress into model. The configured scope
// (buckets, prefix) is left alone: it is not server-reported state.
func applyKmsRekeyStatus(model *kmsRekeyResourceModel, status rustfs.KmsRekeyStatus) {
	model.ID = types.StringValue(status.JobID)
	model.JobID = types.StringValue(status.JobID)
	model.State = types.StringValue(status.State)
	model.CurrentBucket = stringFromPtr(status.CurrentBucket)
	model.Scanned = types.Int64Value(int64(status.Scanned))
	model.Rewrapped = types.Int64Value(int64(status.Rewrapped))
	model.AlreadyCurrent = types.Int64Value(int64(status.AlreadyCurrent))
	model.NotApplicable = types.Int64Value(int64(status.NotApplicable))
	model.Failed = types.Int64Value(int64(status.Failed))
}

func init() {
	registerResource(NewKmsRekeyResource)
}
