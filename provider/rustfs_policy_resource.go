// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Data models.
type policyStatementModel struct {
	Effect      string                         `tfsdk:"effect"`
	Sid         types.String                   `tfsdk:"sid"`
	Action      []string                       `tfsdk:"action"`
	NotAction   []string                       `tfsdk:"not_action"`
	Resource    []string                       `tfsdk:"resource"`
	NotResource []string                       `tfsdk:"not_resource"`
	Condition   map[string]map[string][]string `tfsdk:"condition"`
}

type policyResourceModel struct {
	Name      types.String           `tfsdk:"name"`
	Version   types.String           `tfsdk:"version"`
	Statement []policyStatementModel `tfsdk:"statement"`
}

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &PolicyResource{}
	_ resource.ResourceWithImportState = &PolicyResource{}
)

// NewPolicyResource is a helper function to simplify the provider implementation.
func NewPolicyResource() resource.Resource {
	return &PolicyResource{}
}

// PolicyResource is the resource implementation.
type PolicyResource struct {
	client *AllClient
}

// Metadata returns the resource type name.
func (r *PolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy"
}

// Schema defines the schema for the resource.
func (r *PolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage S3 policies",
		MarkdownDescription: "Manage S3 policies",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the policy",
			},
			"version": schema.StringAttribute{
				Computed: true,
				Default:  stringdefault.StaticString("2012-10-17"),
			},
			"statement": schema.ListNestedAttribute{
				Required:    true,
				Description: "Statements of the policy document",
				NestedObject: schema.NestedAttributeObject{
					Validators: []validator.Object{policyStatementValidator{}},
					Attributes: map[string]schema.Attribute{
						"effect": schema.StringAttribute{
							Required:    true,
							Description: "Whether the statement allows (Allow) or denies (Deny) access",
						},
						"sid": schema.StringAttribute{
							Optional:    true,
							Description: "Optional statement identifier",
						},
						"action": schema.SetAttribute{
							ElementType: types.StringType,
							Optional:    true,
							Description: "Actions the statement applies to. Required unless not_action is set",
						},
						"not_action": schema.SetAttribute{
							ElementType: types.StringType,
							Optional:    true,
							Description: "Actions the statement does not apply to. Required unless action is set",
						},
						"resource": schema.SetAttribute{
							ElementType: types.StringType,
							Optional:    true,
							Description: "Resources the statement applies to",
						},
						"not_resource": schema.SetAttribute{
							ElementType: types.StringType,
							Optional:    true,
							Description: "Resources the statement does not apply to",
						},
						"condition": schema.MapAttribute{
							ElementType: types.MapType{ElemType: types.ListType{ElemType: types.StringType}},
							Optional:    true,
							Description: "Conditions that must be met for the statement to apply, as a map of condition operator (for example StringLike) to a map of condition key to its value list",
						},
					},
				},
			},
		},
	}
}

func (r *PolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *PolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Retrieve values from plan
	var plan policyResourceModel
	diags := req.Plan.Get(ctx, &plan)

	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	policy := rustfs.Policy{
		Version:   plan.Version.ValueString(),
		Name:      plan.Name.ValueString(),
		Statement: policyStatementsFromModel(plan.Statement),
	}
	err := r.client.RustClient.CreatePolicy(ctx, policy)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating policy",
			"Could not create policy, unexpected error: "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "created a resource")
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)

}

// Read refreshes the Terraform state with the latest data.
func (r *PolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state policyResourceModel
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
	actual, err := r.client.RustClient.ReadPolicy(ctx, state.Name.ValueString())
	if err != nil {
		// RustFS answers 404 NoSuchResource for a policy it does not have.
		if rustfs.IsAPIErrorCode(err, "NoSuchResource") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading policy",
			"Could not read policy, unexpected error: "+err.Error(),
		)
		return
	}

	state.Name = types.StringValue(actual.Name)
	state.Version = types.StringValue(actual.Version)
	state.Statement = policyStatementModelsFromAPI(actual.Statement)
	// Save update status
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *PolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan policyResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	policy := rustfs.Policy{
		Version:   plan.Version.ValueString(),
		Name:      plan.Name.ValueString(),
		Statement: policyStatementsFromModel(plan.Statement),
	}
	err := r.client.RustClient.CreatePolicy(ctx, policy)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error updating policy",
			"Could not update policy, unexpected error: "+err.Error(),
		)
		return
	}

	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Delete deletes the resource and removes the Terraform state on success.
func (r *PolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data policyResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RustClient.DeletePolicy(ctx, data.Name.ValueString())
	if err != nil {
		// RustFS answers 200 for an already-deleted policy, but treat an
		// explicit NoSuchResource the same way if a server ever returns it.
		if rustfs.IsAPIErrorCode(err, "NoSuchResource") {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting policy",
			"Could not delete policy, unexpected error: "+err.Error(),
		)
		return
	}
}

func (r *PolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

const policyStatementValidatorDescription = "Ensures every statement sets either action or not_action"

// policyStatementValidator requires every statement to carry an action or a
// not_action. objectvalidator.AtLeastOneOf cannot express this rule for the
// elements of a nested attribute list: it returns early whenever the object it
// validates is not null, which always holds for a list element.
type policyStatementValidator struct{}

var _ validator.Object = policyStatementValidator{}

func (policyStatementValidator) Description(_ context.Context) string {
	return policyStatementValidatorDescription
}

func (policyStatementValidator) MarkdownDescription(_ context.Context) string {
	return policyStatementValidatorDescription
}

func (policyStatementValidator) ValidateObject(_ context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	attributes := req.ConfigValue.Attributes()

	action, hasAction := attributes["action"]
	notAction, hasNotAction := attributes["not_action"]
	if !hasAction || !hasNotAction {
		return
	}

	// A statement whose action is still unknown cannot be validated yet.
	if action.IsUnknown() || notAction.IsUnknown() {
		return
	}
	if !action.IsNull() || !notAction.IsNull() {
		return
	}

	resp.Diagnostics.AddAttributeError(
		req.Path.AtName("not_action"),
		"Invalid policy statement",
		"A policy statement must set either action or not_action.",
	)
}

// policyStatementsFromModel builds the statement list of the policy document
// sent to the API from the planned statement blocks.
func policyStatementsFromModel(statements []policyStatementModel) []rustfs.PolicyStatement {
	out := make([]rustfs.PolicyStatement, 0, len(statements))
	for _, statement := range statements {
		out = append(out, rustfs.PolicyStatement{
			Effect:      statement.Effect,
			Sid:         statement.Sid.ValueString(),
			Action:      statement.Action,
			NotAction:   statement.NotAction,
			Resource:    statement.Resource,
			NotResource: statement.NotResource,
			Condition:   rustfs.PolicyCondition(statement.Condition),
		})
	}
	return out
}

// policyStatementModelsFromAPI converts the statements of a policy document
// read from the API into statement blocks. An unset statement identifier
// becomes null so it does not differ from a configuration that omits sid.
func policyStatementModelsFromAPI(statements []rustfs.PolicyStatement) []policyStatementModel {
	out := make([]policyStatementModel, 0, len(statements))
	for _, statement := range statements {
		sid := types.StringNull()
		if statement.Sid != "" {
			sid = types.StringValue(statement.Sid)
		}
		out = append(out, policyStatementModel{
			Effect:      statement.Effect,
			Sid:         sid,
			Action:      statement.Action,
			NotAction:   statement.NotAction,
			Resource:    statement.Resource,
			NotResource: statement.NotResource,
			Condition:   statement.Condition,
		})
	}
	return out
}

func init() {
	registerResource(NewPolicyResource)
}
