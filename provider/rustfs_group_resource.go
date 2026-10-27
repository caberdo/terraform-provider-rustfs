// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &GroupResource{}
	_ resource.ResourceWithImportState = &GroupResource{}
)

type GroupResource struct {
	client *AllClient
}

type GroupResourceModel struct {
	Name    types.String `tfsdk:"name"`
	Status  types.String `tfsdk:"status"`
	Members types.Set    `tfsdk:"members"`
}

func NewGroupResource() resource.Resource {
	return &GroupResource{}
}

func (r *GroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *GroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage RustFS IAM groups",
		MarkdownDescription: "Manage RustFS IAM groups and their members",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Group name. Changing this forces a new resource to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"status": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Group status: enabled or disabled. Defaults to enabled.",
				Default:     stringdefault.StaticString("enabled"),
			},
			"members": schema.SetAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Description: "Set of user access keys that are members of this group.",
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *GroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T.", req.ProviderData),
		)
		return
	}
	r.client = client
}

func (r *GroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan GroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var members []string
	if !plan.Members.IsUnknown() && !plan.Members.IsNull() {
		resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &members, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	status := plan.Status.ValueString()
	if status == "" {
		status = "enabled"
	}

	// The admin API creates a group implicitly via update-group-members, so it
	// must always be called (even with zero members) to make the group exist.
	err := r.client.RustClient.UpdateGroupMembers(ctx, rustfs.GroupAddRemove{
		Group:    plan.Name.ValueString(),
		Members:  members,
		IsRemove: false,
		Status:   status,
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating group",
			"Could not create group: "+err.Error(),
		)
		return
	}

	if status != "enabled" {
		if err := r.client.RustClient.SetGroupStatus(ctx, plan.Name.ValueString(), status); err != nil {
			resp.Diagnostics.AddError(
				"Error setting group status",
				"Could not set group status: "+err.Error(),
			)
			return
		}
	}

	membersValue, diags := types.SetValueFrom(ctx, types.StringType, members)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Members = membersValue
	plan.Status = types.StringValue(status)
	tflog.Trace(ctx, "created group resource")
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *GroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state GroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := r.client.RustClient.GetGroup(ctx, state.Name.ValueString())
	if err != nil {
		if rustfs.IsAPIErrorCode(err, "NoSuchResource", "NoSuchGroup") {
			// The group was removed outside Terraform, so the next plan has to
			// recreate it instead of failing the refresh.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading group",
			"Could not read group: "+err.Error(),
		)
		return
	}

	state.Status = types.StringValue(info.Status)
	members, diags := types.SetValueFrom(ctx, types.StringType, info.Members)
	resp.Diagnostics.Append(diags...)
	state.Members = members

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *GroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state GroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var planMembers []string
	if !plan.Members.IsUnknown() && !plan.Members.IsNull() {
		resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &planMembers, false)...)
	}
	var stateMembers []string
	if !state.Members.IsUnknown() && !state.Members.IsNull() {
		resp.Diagnostics.Append(state.Members.ElementsAs(ctx, &stateMembers, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Members.IsUnknown() {
		// The configuration leaves members to the provider, so the membership
		// already on the server is kept: a nil delta would otherwise read as
		// "remove every member" and the apply could not return a known value.
		plan.Members = state.Members
		planMembers = stateMembers
	}

	name := plan.Name.ValueString()

	// Membership is applied as a delta: the admin API adds and removes members
	// over the same route with opposite isRemove flags, and it refuses to
	// delete a group that still has members, so the group itself is kept.
	var remove, add []string
	for _, member := range stateMembers {
		if !slices.Contains(planMembers, member) {
			remove = append(remove, member)
		}
	}
	for _, member := range planMembers {
		if !slices.Contains(stateMembers, member) {
			add = append(add, member)
		}
	}

	if len(remove) > 0 {
		if err := r.client.RustClient.UpdateGroupMembers(ctx, rustfs.GroupAddRemove{
			Group:    name,
			Members:  remove,
			IsRemove: true,
		}); err != nil {
			resp.Diagnostics.AddError(
				"Error updating group",
				"Could not remove group members: "+err.Error(),
			)
			return
		}
	}

	if len(add) > 0 {
		status := plan.Status.ValueString()
		if status == "" {
			status = "enabled"
		}
		if err := r.client.RustClient.UpdateGroupMembers(ctx, rustfs.GroupAddRemove{
			Group:    name,
			Members:  add,
			IsRemove: false,
			Status:   status,
		}); err != nil {
			resp.Diagnostics.AddError(
				"Error updating group members",
				"Could not add group members: "+err.Error(),
			)
			return
		}
	}

	// status has a schema default, so it is always known: only push it when it
	// actually differs from the state, otherwise a members-only update issues a
	// pointless call that would also undo an out-of-band status change.
	if plan.Status.ValueString() != state.Status.ValueString() {
		if err := r.client.RustClient.SetGroupStatus(ctx, name, plan.Status.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Error updating group status",
				"Could not set group status: "+err.Error(),
			)
			return
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *GroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data GroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := data.Name.ValueString()

	if err := r.client.RustClient.DeleteGroup(ctx, name); err != nil {
		switch {
		case rustfs.IsAPIErrorCode(err, "NoSuchResource", "NoSuchGroup"):
			// Already gone, so there is nothing left to destroy.
			return
		case isGroupNotEmpty(err):
			// The admin API refuses to delete a group that still has members,
			// and Terraform destroys the group before the users it references,
			// so every member is detached and the delete is retried once.
			info, readErr := r.client.RustClient.GetGroup(ctx, name)
			if readErr != nil {
				resp.Diagnostics.AddError(
					"Error deleting group",
					"Could not read group before detaching its members: "+readErr.Error(),
				)
				return
			}
			if len(info.Members) > 0 {
				if err := r.client.RustClient.UpdateGroupMembers(ctx, rustfs.GroupAddRemove{
					Group:    name,
					Members:  info.Members,
					IsRemove: true,
				}); err != nil {
					resp.Diagnostics.AddError(
						"Error deleting group",
						"Could not remove group members: "+err.Error(),
					)
					return
				}
			}
			if err := r.client.RustClient.DeleteGroup(ctx, name); err != nil &&
				!rustfs.IsAPIErrorCode(err, "NoSuchResource", "NoSuchGroup") {
				resp.Diagnostics.AddError(
					"Error deleting group",
					"Could not delete group: "+err.Error(),
				)
			}
			return
		default:
			resp.Diagnostics.AddError(
				"Error deleting group",
				"Could not delete group: "+err.Error(),
			)
			return
		}
	}
}

// isGroupNotEmpty reports whether err says the group still has members, which is
// the only reason the admin API refuses to delete it. It answers 400
// InvalidRequest with the message "group is not empty"; the code alone is not
// enough because RustFS reuses InvalidRequest for unrelated request failures,
// so the endpoint's own wording is required as well.
func isGroupNotEmpty(err error) bool {
	var apiErr *rustfs.APIError
	return errors.As(err, &apiErr) &&
		apiErr.Code == "InvalidRequest" &&
		strings.Contains(apiErr.Message, "group is not empty")
}

func (r *GroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func init() {
	registerResource(NewGroupResource)
}
