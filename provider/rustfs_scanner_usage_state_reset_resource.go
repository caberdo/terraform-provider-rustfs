// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &ScannerUsageStateResetResource{}
	_ resource.ResourceWithValidateConfig = &ScannerUsageStateResetResource{}
)

// ScannerUsageStateResetResource performs the destructive scanner usage-state
// rebuild. It is an action, not managed state: Read echoes the recorded result
// and Delete is a no-op. The reset needs confirm_full_rebuild because it
// discards the durable usage baseline.
type ScannerUsageStateResetResource struct {
	client *AllClient
}

type ScannerUsageStateResetResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Triggers           types.Map    `tfsdk:"triggers"`
	ConfirmFullRebuild types.Bool   `tfsdk:"confirm_full_rebuild"`
	Status             types.String `tfsdk:"status"`
	Mode               types.String `tfsdk:"mode"`
	UsageState         types.String `tfsdk:"usage_state"`
	LeaderEpoch        types.Int64  `tfsdk:"leader_epoch"`
	NextCycle          types.Int64  `tfsdk:"next_cycle"`
	ResetPaths         types.List   `tfsdk:"reset_paths"`
}

func NewScannerUsageStateResetResource() resource.Resource {
	return &ScannerUsageStateResetResource{}
}

func (r *ScannerUsageStateResetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scanner_usage_state_reset"
}

func (r *ScannerUsageStateResetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	description := "Run the scanner usage-state rebuild (POST /rustfs/admin/v3/scanner/usage-state/reset). " +
		"This is destructive to accounting: the reset discards the durable usage baseline and publishes a " +
		"bootstrap-pending marker, and the next complete scanner cycle rebuilds usage from a full pass. " +
		"Bucket quotas and usage reporting may briefly misreport while that happens. The action is one-shot: " +
		"Read only echoes the recorded result, Delete is a no-op, and changing `triggers` runs it again. " +
		"The route is synchronous; the provider retries a busy scanner leader lock a few times and then fails, " +
		"so retry the apply after the current cycle ends if the lock stays busy. Scanner tuning stays in the " +
		"`scanner` subsystem of `rustfs_config` or in the server environment."
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
			"confirm_full_rebuild": schema.BoolAttribute{
				Required: true,
				Description: "Must be `true`. Acknowledges that the reset discards the durable usage baseline, " +
					"that quotas and usage reporting can misreport until the rebuild completes, and that the " +
					"scanner must run a full pass to recover it.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Status the server returned for the reset.",
			},
			"mode": schema.StringAttribute{
				Computed:    true,
				Description: "Reset mode the server applied, `full-rebuild`.",
			},
			"usage_state": schema.StringAttribute{
				Computed:    true,
				Description: "Usage state the reset left behind, normally `bootstrap-pending`.",
			},
			"leader_epoch": schema.Int64Attribute{
				Computed:    true,
				Description: "Storage publication epoch the reset was fenced with.",
			},
			"next_cycle": schema.Int64Attribute{
				Computed:    true,
				Description: "Scanner cycle that will perform the rebuild.",
			},
			"reset_paths": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Usage objects the reset cleared or marked.",
			},
		},
	}
}

func (r *ScannerUsageStateResetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ScannerUsageStateResetResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config ScannerUsageStateResetResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.ConfirmFullRebuild.IsNull() || config.ConfirmFullRebuild.IsUnknown() {
		return
	}
	if !config.ConfirmFullRebuild.ValueBool() {
		resp.Diagnostics.AddAttributeError(
			path.Root("confirm_full_rebuild"),
			"Invalid scanner usage-state rebuild confirmation",
			"confirm_full_rebuild must be true: the reset discards the durable usage baseline and quotas may "+
				"misreport until a full scanner pass rebuilds it.",
		)
	}
}

func (r *ScannerUsageStateResetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ScannerUsageStateResetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.ConfirmFullRebuild.ValueBool() {
		resp.Diagnostics.AddAttributeError(
			path.Root("confirm_full_rebuild"),
			"Invalid scanner usage-state rebuild confirmation",
			"confirm_full_rebuild must be true.",
		)
		return
	}

	r.runReset(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ScannerUsageStateResetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ScannerUsageStateResetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ScannerUsageStateResetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ScannerUsageStateResetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.ConfirmFullRebuild.ValueBool() {
		resp.Diagnostics.AddAttributeError(
			path.Root("confirm_full_rebuild"),
			"Invalid scanner usage-state rebuild confirmation",
			"confirm_full_rebuild must be true.",
		)
		return
	}

	r.runReset(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ScannerUsageStateResetResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *ScannerUsageStateResetResource) runReset(
	ctx context.Context,
	model *ScannerUsageStateResetResourceModel,
	diagnostics *diag.Diagnostics,
) {
	result, err := r.resetUsageState(ctx)
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			diagnostics.AddError(
				"Scanner usage-state reset is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_scanner_usage_state_reset resource from the configuration.",
			)
			return
		}
		diagnostics.AddError("Error resetting scanner usage state", "Could not reset scanner usage state: "+err.Error())
		return
	}

	resetPaths, listDiags := types.ListValueFrom(ctx, types.StringType, result.ResetPaths)
	diagnostics.Append(listDiags...)
	if diagnostics.HasError() {
		return
	}

	model.ID = types.StringValue("scanner-usage-state-reset")
	model.Status = types.StringValue(result.Status)
	model.Mode = types.StringValue(result.Mode)
	model.UsageState = types.StringValue(result.UsageState)
	model.LeaderEpoch = types.Int64Value(int64(result.LeaderEpoch))
	model.NextCycle = types.Int64Value(int64(result.NextCycle))
	model.ResetPaths = resetPaths
}

// resetUsageState retries the synchronous reset while the scanner leader still
// holds the state lock. The async recovery-intent form is deliberately not
// used, so a lock that never frees fails the apply with the server's error.
func (r *ScannerUsageStateResetResource) resetUsageState(ctx context.Context) (rustfs.ScannerUsageStateResetResult, error) {
	return resetWithScannerLeaderRetry(ctx, func() (rustfs.ScannerUsageStateResetResult, error) {
		return r.client.RustClient.ResetScannerUsageState(ctx)
	})
}

func init() {
	registerResource(NewScannerUsageStateResetResource)
}
