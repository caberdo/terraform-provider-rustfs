// Copyright (c) 2026 the terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &TokenRevocationResource{}

// TokenRevocationResource revokes every STS credential one parent user holds
// under one identity provider. It is an action, not managed state: Read echoes
// the recorded result, Delete is a no-op, and revocation cannot be undone.
type TokenRevocationResource struct {
	client *AllClient
}

type TokenRevocationResourceModel struct {
	ID           types.String `tfsdk:"id"`
	UserProvider types.String `tfsdk:"user_provider"`
	User         types.String `tfsdk:"user"`
	Triggers     types.Map    `tfsdk:"triggers"`
	Revoked      types.Int64  `tfsdk:"revoked"`
}

func NewTokenRevocationResource() resource.Resource {
	return &TokenRevocationResource{}
}

func (r *TokenRevocationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_token_revocation"
}

func (r *TokenRevocationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	description := "Revoke a RustFS user's temporary (STS) access keys for one identity provider " +
		"(POST /rustfs/admin/v3/revoke-tokens/{user_provider}).\n\n" +
		"**This action logs users out immediately.** Creating the resource deletes every STS credential " +
		"`user` holds under `user_provider`; the next request signed with such a credential fails " +
		"authentication. The revocation cannot be undone, so the resource has to name both the provider " +
		"and the user: RustFS would otherwise default to the caller's own identity, and a provider-wide " +
		"revocation is not what a typo should do.\n\n" +
		"The resource is an action, not managed state: Read only echoes the recorded result, Delete is a " +
		"no-op, and changing `user_provider`, `user` or `triggers` replaces the resource and revokes again. " +
		"Revocation is idempotent at the server: with no matching STS credentials left it reports zero " +
		"revoked and succeeds."
	resp.Schema = schema.Schema{
		Description:         "Revoke a RustFS user's temporary (STS) access keys for one identity provider",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "`<user_provider>:<user>`.",
			},
			"user_provider": schema.StringAttribute{
				Required:    true,
				Description: "Identity provider whose STS credentials are revoked: `builtin`, `ldap` or `openid` (`openid` covers OIDC). Changing it revokes again.",
				Validators: []validator.String{
					stringvalidator.OneOf(
						rustfs.UserProviderBuiltin,
						rustfs.UserProviderLDAP,
						rustfs.UserProviderOpenID,
					),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user": schema.StringAttribute{
				Required: true,
				Description: "Parent user whose STS credentials are revoked. It is required: omitting it " +
					"would make the server revoke the caller's own tokens. Changing it revokes the new " +
					"user and leaves the old revocation in place.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"triggers": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Arbitrary values that are not sent to the server. Change one to revoke again; " +
					"without a change Terraform does not repeat the revocation.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"revoked": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of STS credentials the server reported deleting.",
			},
		},
	}
}

func (r *TokenRevocationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TokenRevocationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TokenRevocationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.revoke(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read echoes the recorded result. RustFS has no route that reads a past
// revocation back, and the deleted credentials are gone, so a refresh must not
// retry the action.
func (r *TokenRevocationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TokenRevocationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update reruns the revocation. Every input carries RequiresReplace, so the
// framework normally replaces instead of updating; rerunning keeps the
// resource correct if that ever changes.
func (r *TokenRevocationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TokenRevocationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.revoke(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete is a no-op: a revocation cannot be undone. Destroying the resource
// only forgets that the revocation happened.
func (r *TokenRevocationResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *TokenRevocationResource) revoke(
	ctx context.Context,
	model *TokenRevocationResourceModel,
	diagnostics *diag.Diagnostics,
) {
	result, err := r.client.RustClient.RevokeTokens(ctx, model.UserProvider.ValueString(), model.User.ValueString())
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			diagnostics.AddError(
				"Token revocation is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_token_revocation resource from the configuration.",
			)
			return
		}
		diagnostics.AddError(
			"Error revoking temporary credentials",
			"Could not revoke temporary credentials for "+model.User.ValueString()+" ("+
				model.UserProvider.ValueString()+"): "+err.Error(),
		)
		return
	}

	model.ID = types.StringValue(model.UserProvider.ValueString() + ":" + model.User.ValueString())
	model.Revoked = types.Int64Value(result.Revoked)
	tflog.Trace(ctx, "revoked temporary credentials", map[string]any{
		"user_provider": model.UserProvider.ValueString(),
		"revoked":       result.Revoked,
	})
}

func init() {
	registerResource(NewTokenRevocationResource)
}
