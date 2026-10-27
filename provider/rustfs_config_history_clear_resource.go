// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &ConfigHistoryClearResource{}
	_ resource.ResourceWithValidateConfig = &ConfigHistoryClearResource{}
)

const (
	configHistoryClearID           = "config-history-clear"
	configHistoryClearConfirmation = "clear-config-history"
)

// ConfigHistoryClearResource deletes every stored config snapshot. It is an
// action, not managed state: Read echoes the recorded result and Delete is a
// no-op, because destroy must never delete history a later operator still
// needs.
type ConfigHistoryClearResource struct {
	client *AllClient
}

type ConfigHistoryClearResourceModel struct {
	ID        types.String `tfsdk:"id"`
	Confirm   types.String `tfsdk:"confirm"`
	Triggers  types.Map    `tfsdk:"triggers"`
	ClearedAt types.String `tfsdk:"cleared_at"`
}

func NewConfigHistoryClearResource() resource.Resource {
	return &ConfigHistoryClearResource{}
}

func (r *ConfigHistoryClearResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config_history_clear"
}

func (r *ConfigHistoryClearResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	description := "Delete every stored server-config snapshot (DELETE /rustfs/admin/v3/clear-config-history-kv). " +
		"This is destructive: all rollback points recorded in `rustfs_config_history` are deleted, so " +
		"`rustfs_config_restore` can no longer reach them and an earlier configuration mistake can no longer be " +
		"rolled back through the provider. The server returns an empty 200 without a count, so the only recorded " +
		"result is when the clear ran. The action is one-shot: Read only echoes the recorded result, Delete is a " +
		"no-op, and changing `triggers` runs the clear again."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Resource identifier, always `config-history-clear`.",
			},
			"confirm": schema.StringAttribute{
				Required: true,
				Description: "Must be the literal `clear-config-history`. Acknowledges that every rollback point " +
					"is deleted and that `rustfs_config_restore` can no longer reach them.",
			},
			"triggers": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Arbitrary values that are not sent to the server. Change one to clear the history " +
					"again; without a change Terraform does not repeat it.",
			},
			"cleared_at": schema.StringAttribute{
				Computed:    true,
				Description: "When this resource last cleared the config history, in RFC 3339 UTC.",
			},
		},
	}
}

func (r *ConfigHistoryClearResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ConfigHistoryClearResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config ConfigHistoryClearResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateConfigHistoryClearConfirmation(&resp.Diagnostics, config.Confirm)
}

// validateConfigHistoryClearConfirmation enforces the exact acknowledgement
// literal. Unknown values are left for apply time, where they cannot remain
// unknown.
func validateConfigHistoryClearConfirmation(diagnostics *diag.Diagnostics, confirm types.String) {
	if confirm.IsUnknown() {
		return
	}
	if confirm.IsNull() || confirm.ValueString() == "" {
		diagnostics.AddAttributeError(
			path.Root("confirm"),
			"Missing config history clear confirmation",
			"confirm must be the literal \""+configHistoryClearConfirmation+"\": the clear deletes every rollback "+
				"point, and rustfs_config_restore can no longer reach them.",
		)
		return
	}
	if confirm.ValueString() != configHistoryClearConfirmation {
		diagnostics.AddAttributeError(
			path.Root("confirm"),
			"Invalid config history clear confirmation",
			"confirm must be the literal \""+configHistoryClearConfirmation+"\", got \""+confirm.ValueString()+"\": "+
				"the clear deletes every rollback point, and rustfs_config_restore can no longer reach them.",
		)
	}
}

func (r *ConfigHistoryClearResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ConfigHistoryClearResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.applyClear(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ConfigHistoryClearResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ConfigHistoryClearResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The server keeps no record of the clear, and Read must not delete the
	// history again, so it only echoes what Create or Update recorded.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ConfigHistoryClearResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ConfigHistoryClearResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.applyClear(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ConfigHistoryClearResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// Deleted history cannot be restored, and a destroy must never delete the
	// rollback points of a configuration the operator is still changing.
	// Destroy only forgets the record.
}

func (r *ConfigHistoryClearResource) applyClear(ctx context.Context, plan *ConfigHistoryClearResourceModel, diagnostics *diag.Diagnostics) {
	if plan.Confirm.IsUnknown() {
		diagnostics.AddError(
			"Incomplete config history clear",
			"confirm must be resolved before the config history can be cleared.",
		)
		return
	}

	validateConfigHistoryClearConfirmation(diagnostics, plan.Confirm)
	if diagnostics.HasError() {
		return
	}

	if err := r.client.RustClient.ClearConfigHistory(ctx); err != nil {
		diagnostics.AddError(
			"Error clearing config history",
			"Could not clear the server's config history: "+err.Error(),
		)
		return
	}

	plan.ID = types.StringValue(configHistoryClearID)
	plan.ClearedAt = types.StringValue(time.Now().UTC().Format(time.RFC3339))
}

func init() {
	registerResource(NewConfigHistoryClearResource)
}
