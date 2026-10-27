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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &UserPolicyAttachmentResource{}
	_ resource.ResourceWithImportState = &UserPolicyAttachmentResource{}
)

// UserPolicyAttachmentResource attaches one canned IAM policy to a user on
// top of the user's primary policy, so a user can hold more than one policy.
type UserPolicyAttachmentResource struct {
	client *AllClient
}

type UserPolicyAttachmentResourceModel struct {
	User   types.String `tfsdk:"user"`
	Policy types.String `tfsdk:"policy"`
	ID     types.String `tfsdk:"id"`
}

// NewUserPolicyAttachmentResource is a helper function to simplify the provider implementation.
func NewUserPolicyAttachmentResource() resource.Resource {
	return &UserPolicyAttachmentResource{}
}

// Metadata returns the resource type name.
func (r *UserPolicyAttachmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_policy_attachment"
}

// Schema defines the schema for the resource.
func (r *UserPolicyAttachmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Attach a canned IAM policy to a user",
		MarkdownDescription: "Attach a canned IAM policy to a user and detach it on destroy",
		Attributes: map[string]schema.Attribute{
			"user": schema.StringAttribute{
				Required:    true,
				Description: "Access key of the IAM user. Changing this forces a new attachment.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"policy": schema.StringAttribute{
				Required:    true,
				Description: "Name of the canned policy. Changing this forces a new attachment.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Composite identifier in the form user/policy.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *UserPolicyAttachmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create attaches the policy to the user and sets the initial Terraform state.
func (r *UserPolicyAttachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan UserPolicyAttachmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RustClient.AttachUserPolicy(ctx, plan.User.ValueString(), plan.Policy.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error attaching policy to user",
			"Could not attach policy: "+err.Error(),
		)
		return
	}

	plan.ID = types.StringValue(plan.User.ValueString() + "/" + plan.Policy.ValueString())
	tflog.Trace(ctx, "attached policy to user")
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read probes the user the attachment targets. The admin API answers 404
// NoSuchResource for a user it does not have, and user-info returns the user's
// whole policy list in policyName as one comma-separated string (what the
// RustFS console splits to render a user's policies), so a policy missing from
// that list was detached outside Terraform and the attachment is dropped from
// state.
func (r *UserPolicyAttachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state UserPolicyAttachmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	user := state.User.ValueString()
	policy := state.Policy.ValueString()

	account, err := r.client.RustClient.ReadUserAccount(ctx, user)
	if err != nil {
		if isMissingUserError(err) {
			// The user is gone, so the attachment is gone with it.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading user policy attachment",
			"Could not read user "+user+": "+err.Error(),
		)
		return
	}

	if !slices.Contains(splitPolicyNames(account.Policy), policy) {
		tflog.Trace(ctx, "user policy attachment no longer attached", map[string]any{
			// The user is an access key, so only the policy name is logged.
			"policy": policy,
		})
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(user + "/" + policy)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update re-attaches the policy. All attributes are RequiresReplace, so
// Terraform normally plans a replacement; re-attaching keeps this method
// correct if it is invoked.
func (r *UserPolicyAttachmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan UserPolicyAttachmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RustClient.AttachUserPolicy(ctx, plan.User.ValueString(), plan.Policy.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error attaching policy to user",
			"Could not attach policy: "+err.Error(),
		)
		return
	}

	plan.ID = types.StringValue(plan.User.ValueString() + "/" + plan.Policy.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete detaches the policy from the user and removes the Terraform state on success.
func (r *UserPolicyAttachmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data UserPolicyAttachmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RustClient.DetachUserPolicy(ctx, data.User.ValueString(), data.Policy.ValueString())
	if err != nil {
		// A user that no longer exists cannot still carry the policy, so the
		// attachment is already gone. The builtin detach endpoint answers a
		// missing user with 400 InvalidArgument "user not exist" (probed
		// against RustFS latest); newer builds answer 404 NoSuchResource.
		if isMissingUserError(err) {
			return
		}
		resp.Diagnostics.AddError(
			"Error detaching policy from user",
			"Could not detach policy: "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "detached policy from user")
}

// isMissingUserError reports whether err is the admin API's answer for a user
// the server does not have. The structured signal differs per endpoint: reads
// answer 404 NoSuchResource, while the builtin policy attach/detach endpoints
// answer 400 InvalidArgument with the message "user not exist". The code alone
// is not enough there, because InvalidArgument also covers unrelated bad
// requests, so the endpoint's own missing-user text is matched too.
func isMissingUserError(err error) bool {
	if rustfs.IsAPIErrorCode(err, "NoSuchResource") {
		return true
	}
	var apiErr *rustfs.APIError
	return errors.As(err, &apiErr) &&
		apiErr.Code == "InvalidArgument" &&
		strings.EqualFold(strings.TrimSpace(apiErr.Message), "user not exist")
}

// splitPolicyNames splits the admin API's comma-separated policy field into
// names. The same field shape is used by GetGroup (GroupInfo.Policy) and
// user-info (UserAccount.Policy), whose whole value the RustFS console splits
// on "," to render a user's policy list.
func splitPolicyNames(policies string) []string {
	names := []string{}
	for _, name := range strings.Split(policies, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// ImportState supports importing an attachment using the composite
// <user>/<policy> identifier.
func (r *UserPolicyAttachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			"Expected format: <user>/<policy>",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("policy"), parts[1])...)
}

func init() {
	registerResource(NewUserPolicyAttachmentResource)
}
