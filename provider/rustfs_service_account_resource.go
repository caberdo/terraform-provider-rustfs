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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type serviceAccountResourceModel struct {
	AccessKey     types.String `tfsdk:"access_key"`
	SecretKey     types.String `tfsdk:"secret_key"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	TargetUser    types.String `tfsdk:"user"`
	Expiration    types.String `tfsdk:"expiration"`
	Policy        types.String `tfsdk:"policy"`
	ImpliedPolicy types.Bool   `tfsdk:"implied_policy"`
}

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &ServiceAccountResource{}
	_ resource.ResourceWithImportState = &ServiceAccountResource{}
)

// NewServiceAccountResource is a helper function to simplify the provider implementation.
func NewServiceAccountResource() resource.Resource {
	return &ServiceAccountResource{}
}

// ServiceAccountResource is the resource implementation.
type ServiceAccountResource struct {
	client *AllClient
}

// Metadata returns the resource type name.
func (r *ServiceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_serviceaccount"
}

// Schema defines the schema for the resource.
func (r *ServiceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage ServiceUser/API Keys",
		MarkdownDescription: "Manage ServiceUser/API Keys",
		Attributes: map[string]schema.Attribute{
			"access_key": schema.StringAttribute{
				MarkdownDescription: "Access Key",
				Required:            true,
			},
			"secret_key": schema.StringAttribute{
				MarkdownDescription: "Secret Key",
				Required:            true,
				Sensitive:           true,
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Visible name, only for viewing",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Short description of the scope we plan to use this token",
			},
			"user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional user the token should be scoped to. Changing this forces a new resource to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"expiration": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("9999-01-01T00:00:00Z"),
				MarkdownDescription: "Expiration timestamp in RFC3339 format without fractional seconds (e.g. 2030-01-01T00:00:00Z). Defaults to 9999-01-01T00:00:00Z.",
			},
			"policy": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "IAM policy document (JSON) the service account is scoped to. Changing this forces a new resource to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"implied_policy": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the implied policy is used (true when no explicit policy is set).",
			},
		},
	}
}

func (r *ServiceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create creates the resource and sets the initial Terraform state.
func (r *ServiceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Retrieve values from plan
	var plan serviceAccountResourceModel
	diags := req.Plan.Get(ctx, &plan)

	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	account := rustfs.ServiceAccount{
		Name:        plan.Name.ValueString(),
		AccessKey:   plan.AccessKey.ValueString(),
		SecretKey:   plan.SecretKey.ValueString(),
		Description: plan.Description.ValueString(),
		TargetUser:  plan.TargetUser.ValueString(),
		Expiration:  plan.Expiration.ValueString(),
		Policy:      plan.Policy.ValueString(),
	}
	err := r.client.RustClient.CreateServiceAccount(ctx, account)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating service account",
			"Could not create service account, unexpected error: "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "created a resource")
	plan.ImpliedPolicy = types.BoolValue(plan.Policy.ValueString() == "")
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)

}

// Read refreshes the Terraform state with the latest data.
func (r *ServiceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Make the read request
	actual, err := r.client.RustClient.ReadServiceAccount(ctx, state.AccessKey.ValueString())
	if err != nil {
		if isMissingServiceAccount(err) {
			// Dropping it from state makes Terraform plan a recreation instead
			// of failing the refresh.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading service account",
			"Could not read service account, unexpected error: "+err.Error(),
		)
		return
	}

	state.Name = types.StringValue(actual.Name)
	if actual.Description != "" {
		state.Description = types.StringValue(actual.Description)
	}
	if actual.Expiration != "" {
		state.Expiration = types.StringValue(actual.Expiration)
	}
	state.ImpliedPolicy = types.BoolValue(actual.ImpliedPolicy)
	if !actual.ImpliedPolicy && state.Policy.IsNull() {
		state.Policy = types.StringValue(actual.Policy)
	}
	// Save update status
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *ServiceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serviceAccountResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	account := rustfs.ServiceAccount{
		Name:        plan.Name.ValueString(),
		AccessKey:   plan.AccessKey.ValueString(),
		SecretKey:   plan.SecretKey.ValueString(),
		Description: plan.Description.ValueString(),
		TargetUser:  plan.TargetUser.ValueString(),
		Expiration:  plan.Expiration.ValueString(),
		Policy:      plan.Policy.ValueString(),
	}
	err := r.client.RustClient.UpdateServiceAccount(ctx, account)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error updating service account",
			"Could not update service account, unexpected error: "+err.Error(),
		)
		return
	}

	plan.Name = types.StringValue(account.Name)
	if account.Description != "" {
		plan.Description = types.StringValue(account.Description)
	}
	plan.Expiration = types.StringValue(account.Expiration)
	plan.ImpliedPolicy = types.BoolValue(account.Policy == "")

	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Delete deletes the resource and removes the Terraform state on success.
func (r *ServiceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data serviceAccountResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}
	account := rustfs.ServiceAccount{
		Name:        data.Name.ValueString(),
		AccessKey:   data.AccessKey.ValueString(),
		SecretKey:   data.SecretKey.ValueString(),
		Description: data.Description.ValueString(),
	}
	err := r.client.RustClient.DeleteServiceAccount(ctx, account)
	if err != nil {
		if isMissingServiceAccount(err) {
			// Already gone: destroying twice must not fail.
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting service account",
			"Could not delete service account, unexpected error: "+err.Error(),
		)
	}
}

// isMissingServiceAccount reports whether err says the service account is not
// on the server, whichever endpoint answered. Both signals were probed against
// a running RustFS: the read endpoints (info-service-account) answer 404
// NoSuchResource, while the delete endpoint (delete-service-accounts) has no
// structured missing-object code and answers 400 InvalidRequest with the
// message "service account not exist". The code alone is not enough there —
// RustFS reuses InvalidRequest for unrelated request failures — so the
// endpoint's own wording is required as well. Read and Delete share the
// predicate so an account removed out of band is dropped from state and a
// repeated destroy stays a no-op.
func isMissingServiceAccount(err error) bool {
	if rustfs.IsAPIErrorCode(err, "NoSuchResource") {
		return true
	}
	var apiErr *rustfs.APIError
	return errors.As(err, &apiErr) &&
		apiErr.Code == "InvalidRequest" &&
		strings.Contains(apiErr.Message, "service account not exist")
}

func (r *ServiceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("access_key"), req, resp)
}

func init() {
	registerResource(NewServiceAccountResource)
}
