// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &ConfigRestoreResource{}
	_ resource.ResourceWithValidateConfig = &ConfigRestoreResource{}
)

// ConfigRestoreResource restores one stored config snapshot. It is an action,
// not managed state: Read echoes the recorded result and Delete is a no-op,
// because the server has no route that un-restores a configuration.
type ConfigRestoreResource struct {
	client *AllClient
}

type ConfigRestoreResourceModel struct {
	ID         types.String `tfsdk:"id"`
	RestoreID  types.String `tfsdk:"restore_id"`
	Confirm    types.String `tfsdk:"confirm"`
	Triggers   types.Map    `tfsdk:"triggers"`
	RestoredAt types.String `tfsdk:"restored_at"`
}

func NewConfigRestoreResource() resource.Resource {
	return &ConfigRestoreResource{}
}

func (r *ConfigRestoreResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config_restore"
}

func (r *ConfigRestoreResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	description := "Restore a stored server-config snapshot (PUT /rustfs/admin/v3/restore-config-history-kv). " +
		"This is destructive: the snapshot replaces the whole live configuration, so it can undo an unrelated " +
		"change another operator made after the snapshot was taken. RustFS saves the current configuration as a " +
		"new history entry before applying the restore, so the restore itself can be undone through " +
		"`rustfs_config_restore` and the entry that request created. The action is one-shot: Read only echoes the " +
		"recorded result, Delete is a no-op, and changing `triggers` runs the same restore again. `restore_id` must " +
		"name an entry from `rustfs_config_history`; a missing or legacy entry fails the apply and changes nothing."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Resource identifier, the restored history entry.",
			},
			"restore_id": schema.StringAttribute{
				Required: true,
				Description: "Identifier of the history entry to restore, as returned by `rustfs_config_history`. " +
					"Changing it restores a different snapshot.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"confirm": schema.StringAttribute{
				Required: true,
				Description: "Must repeat `restore_id` exactly. Acknowledges that the restore overwrites the whole " +
					"live configuration, not only the scope whose change is being undone.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"triggers": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Arbitrary values that are not sent to the server. Change one to run the same restore " +
					"again; without a change Terraform does not repeat it.",
			},
			"restored_at": schema.StringAttribute{
				Computed:    true,
				Description: "When this resource last applied the restore, in RFC 3339.",
			},
		},
	}
}

func (r *ConfigRestoreResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ConfigRestoreResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config ConfigRestoreResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateConfigRestoreConfirmation(&resp.Diagnostics, config.RestoreID, config.Confirm)
}

// validateConfigRestoreConfirmation appends the explicit-acknowledgement
// diagnostic when confirm does not repeat restore_id. Unknown values are left
// for apply time, where they cannot remain unknown.
func validateConfigRestoreConfirmation(diagnostics *diag.Diagnostics, restoreID, confirm types.String) {
	if restoreID.IsNull() || restoreID.IsUnknown() || confirm.IsNull() || confirm.IsUnknown() {
		return
	}
	if confirm.ValueString() != restoreID.ValueString() {
		diagnostics.AddAttributeError(
			path.Root("confirm"),
			"Invalid config restore confirmation",
			"confirm must repeat restore_id exactly ("+restoreID.ValueString()+"): the restore replaces the whole "+
				"live configuration, including changes another operator made after that snapshot.",
		)
	}
}

func (r *ConfigRestoreResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ConfigRestoreResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.applyRestore(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ConfigRestoreResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ConfigRestoreResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The server has no route that reports which snapshot is live, and the
	// restore is not undone by a later clear of the history entry, so Read
	// keeps the record instead of guessing.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ConfigRestoreResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ConfigRestoreResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.applyRestore(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ConfigRestoreResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// A restore cannot be undone, and the provider must not resurrect a
	// configuration the server has since moved past. Destroy only forgets the
	// record; restore the pre-restore entry to roll back.
}

func (r *ConfigRestoreResource) applyRestore(ctx context.Context, plan *ConfigRestoreResourceModel, diagnostics *diag.Diagnostics) {
	if plan.RestoreID.IsUnknown() || plan.Confirm.IsUnknown() {
		diagnostics.AddError(
			"Incomplete config restore",
			"restore_id and confirm must be resolved before the restore can run.",
		)
		return
	}

	validateConfigRestoreConfirmation(diagnostics, plan.RestoreID, plan.Confirm)
	if diagnostics.HasError() {
		return
	}

	restoreID := plan.RestoreID.ValueString()
	if err := r.client.RustClient.RestoreConfigHistory(ctx, restoreID); err != nil {
		diagnostics.AddError(
			"Error restoring config history",
			"Could not restore config history entry "+restoreID+": "+err.Error(),
		)
		return
	}

	plan.ID = types.StringValue(restoreID)
	plan.RestoredAt = types.StringValue(time.Now().UTC().Format(time.RFC3339))
}

func init() {
	registerResource(NewConfigRestoreResource)
}
