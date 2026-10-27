// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &ScannerCycleStateResetResource{}

// ScannerCycleStateResetResource performs the break-glass scanner cycle-state
// reset. It is an action, not managed state: Read echoes what Create recorded
// and Delete does nothing, because the admin API has no route that inspects or
// reverts a reset. Re-running the action requires a deliberate change to
// triggers.
type ScannerCycleStateResetResource struct {
	client *AllClient
}

type ScannerCycleStateResetResourceModel struct {
	ID       types.String `tfsdk:"id"`
	Triggers types.Map    `tfsdk:"triggers"`
	Status   types.String `tfsdk:"status"`
	Mode     types.String `tfsdk:"mode"`
}

func NewScannerCycleStateResetResource() resource.Resource {
	return &ScannerCycleStateResetResource{}
}

func (r *ScannerCycleStateResetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scanner_cycle_state_reset"
}

func (r *ScannerCycleStateResetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	description := "Run the scanner cycle-state reset (POST /rustfs/admin/v3/scanner/cycle-state/reset). " +
		"Creating this resource clears the scanner's cycle-recovery bookkeeping and makes the next cycle " +
		"scan the full namespace; it does not discard usage accounting. The action is one-shot: Read only " +
		"echoes the recorded result, Delete is a no-op, and changing `triggers` runs the reset again. " +
		"The provider retries a busy scanner leader lock a few times before failing. " +
		"Scanner tuning stays in the `scanner` subsystem of `rustfs_config` or in the server environment."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Reset trigger identifier.",
			},
			"triggers": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Arbitrary values that are not sent to the server. Change one to run the reset again; " +
					"without a change Terraform does not repeat it.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Status the server returned for the reset.",
			},
			"mode": schema.StringAttribute{
				Computed:    true,
				Description: "Reset mode the server applied, `full-rescan`.",
			},
		},
	}
}

func (r *ScannerCycleStateResetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ScannerCycleStateResetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ScannerCycleStateResetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.runReset(ctx, "Error resetting scanner cycle state", &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ScannerCycleStateResetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ScannerCycleStateResetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ScannerCycleStateResetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ScannerCycleStateResetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.runReset(ctx, "Error resetting scanner cycle state", &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ScannerCycleStateResetResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// runReset calls the reset route and fills the model. It is shared by Create
// and Update so a trigger change has exactly the same effect as the first run.
func (r *ScannerCycleStateResetResource) runReset(
	ctx context.Context,
	summary string,
	model *ScannerCycleStateResetResourceModel,
	diagnostics *diag.Diagnostics,
) {
	result, err := resetWithScannerLeaderRetry(ctx, func() (rustfs.ScannerCycleStateResetResult, error) {
		return r.client.RustClient.ResetScannerCycleState(ctx)
	})
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			diagnostics.AddError(
				"Scanner cycle-state reset is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_scanner_cycle_state_reset resource from the configuration.",
			)
			return
		}
		diagnostics.AddError(summary, "Could not reset scanner cycle state: "+err.Error())
		return
	}

	model.ID = types.StringValue("scanner-cycle-state-reset")
	model.Status = types.StringValue(result.Status)
	model.Mode = types.StringValue(result.Mode)
}

func init() {
	registerResource(NewScannerCycleStateResetResource)
}
