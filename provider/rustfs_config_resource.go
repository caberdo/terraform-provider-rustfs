// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &ConfigResource{}
	_ resource.ResourceWithImportState = &ConfigResource{}
)

type ConfigResourceModel struct {
	SubSystem types.String `tfsdk:"sub_system"`
	Settings  types.Map    `tfsdk:"settings"`
	ID        types.String `tfsdk:"id"`
}

func NewConfigResource() resource.Resource {
	return &ConfigResource{}
}

type ConfigResource struct {
	client *AllClient
}

func (r *ConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config"
}

func (r *ConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage server sub-system configuration (config-kv)",
		MarkdownDescription: "Manage server sub-system configuration (config-kv), the `mc admin config` equivalent. Each resource manages one sub-system scope.",
		Attributes: map[string]schema.Attribute{
			"sub_system": schema.StringAttribute{
				Required:    true,
				Description: "Sub-system scope to manage, e.g. `notify_webhook` or `notify_webhook:primary`. Changing this forces recreation.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"settings": schema.MapAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Key/value settings applied to the sub-system scope.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Resource identifier (the sub-system scope).",
			},
		},
	}
}

func (r *ConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func configKVsFromSettings(settings types.Map) ([]rustfs.ConfigKV, error) {
	elements := settings.Elements()
	kvs := make([]rustfs.ConfigKV, 0, len(elements))
	for key, value := range elements {
		str, ok := value.(types.String)
		if !ok {
			return nil, fmt.Errorf("unexpected value type for setting %q: %T", key, value)
		}
		kvs = append(kvs, rustfs.ConfigKV{Key: key, Value: str.ValueString()})
	}
	return kvs, nil
}

func settingsFromConfigKVs(ctx context.Context, kvs []rustfs.ConfigKV) types.Map {
	settings := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		settings[kv.Key] = kv.Value
	}
	value, diags := types.MapValueFrom(ctx, types.StringType, settings)
	if diags.HasError() {
		return types.MapNull(types.StringType)
	}
	return value
}

// configScopeGone reports whether the admin API answered that the config scope
// is not configured. Probed against RustFS 1.0.1 and latest, the endpoint
// answers 400 InvalidRequest with exactly these messages:
//
//	get-config-kv   "config subsystem 'x' not found"
//	                "config target 'y' not found for subsystem 'z'"
//	del-config-kv   "unsupported config subsystem 'x'"
//
// The structured code plus the endpoint's own wording keeps an unrelated
// InvalidRequest (a rejected setting, for example) from being mistaken for a
// missing scope, which would drop a live scope from state and turn a destroy
// into a silent no-op.
func configScopeGone(err error) bool {
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "InvalidRequest" {
		return false
	}
	switch {
	case strings.HasPrefix(apiErr.Message, "config subsystem '"),
		strings.HasPrefix(apiErr.Message, "config target '"),
		strings.HasPrefix(apiErr.Message, "unsupported config subsystem"):
		return true
	}
	return false
}

func (r *ConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kvs, err := configKVsFromSettings(plan.Settings)
	if err != nil {
		resp.Diagnostics.AddError("Error parsing settings", err.Error())
		return
	}
	if err := r.client.RustClient.SetConfig(ctx, plan.SubSystem.ValueString(), kvs); err != nil {
		resp.Diagnostics.AddError(
			"Error setting config",
			"Could not set config, unexpected error: "+err.Error(),
		)
		return
	}

	plan.ID = plan.SubSystem
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kvs, err := r.client.RustClient.GetConfig(ctx, state.SubSystem.ValueString())
	if err != nil {
		if configScopeGone(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading config",
			"Could not read config, unexpected error: "+err.Error(),
		)
		return
	}

	// A successful read with no rendered settings is not evidence that the
	// scope is gone: get-config-kv answers the scope's current values, and a
	// live scope whose settings are all defaults/empty renders no lines (probed
	// on 1.0.1 and latest: an unset subsystem returns eight default keys, and a
	// scope that does not exist at all answers 400 InvalidRequest instead). Only
	// that structured answer removes the resource; an empty body keeps state.
	state.Settings = settingsFromConfigKVs(ctx, kvs)
	state.ID = state.SubSystem
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kvs, err := configKVsFromSettings(plan.Settings)
	if err != nil {
		resp.Diagnostics.AddError("Error parsing settings", err.Error())
		return
	}
	if err := r.client.RustClient.SetConfig(ctx, plan.SubSystem.ValueString(), kvs); err != nil {
		resp.Diagnostics.AddError(
			"Error updating config",
			"Could not update config, unexpected error: "+err.Error(),
		)
		return
	}

	plan.ID = plan.SubSystem
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.RustClient.DeleteConfig(ctx, data.SubSystem.ValueString()); err != nil {
		if configScopeGone(err) {
			// The scope is already gone; del-config-kv answers 400
			// InvalidRequest "unsupported config subsystem" rather than
			// succeeding silently (probed against RustFS latest).
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting config",
			"Could not delete config, unexpected error: "+err.Error(),
		)
	}
}

func (r *ConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("sub_system"), req, resp)
}

func init() {
	registerResource(NewConfigResource)
}
