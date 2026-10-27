// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &OIDCProviderResource{}
	_ resource.ResourceWithImportState = &OIDCProviderResource{}
)

// oidcProviderIDPattern is the server's own provider-id rule
// (alphanumeric, underscore and hyphen).
var oidcProviderIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func NewOIDCProviderResource() resource.Resource {
	return &OIDCProviderResource{}
}

// OIDCProviderResource manages one persisted OIDC provider for RustFS Console
// single sign-on.
type OIDCProviderResource struct {
	client *AllClient
}

type OIDCProviderResourceModel struct {
	ID                     types.String `tfsdk:"id"`
	Enabled                types.Bool   `tfsdk:"enabled"`
	DisplayName            types.String `tfsdk:"display_name"`
	ConfigURL              types.String `tfsdk:"config_url"`
	Issuer                 types.String `tfsdk:"issuer"`
	ClientID               types.String `tfsdk:"client_id"`
	ClientSecret           types.String `tfsdk:"client_secret"`
	ClientSecretConfigured types.Bool   `tfsdk:"client_secret_configured"`
	Scopes                 types.List   `tfsdk:"scopes"`
	OtherAudiences         types.List   `tfsdk:"other_audiences"`
	RedirectURI            types.String `tfsdk:"redirect_uri"`
	RedirectURIDynamic     types.Bool   `tfsdk:"redirect_uri_dynamic"`
	ClaimName              types.String `tfsdk:"claim_name"`
	ClaimPrefix            types.String `tfsdk:"claim_prefix"`
	RolePolicy             types.String `tfsdk:"role_policy"`
	GroupsClaim            types.String `tfsdk:"groups_claim"`
	RolesClaim             types.String `tfsdk:"roles_claim"`
	EmailClaim             types.String `tfsdk:"email_claim"`
	UsernameClaim          types.String `tfsdk:"username_claim"`
	HideFromUI             types.Bool   `tfsdk:"hide_from_ui"`
	Source                 types.String `tfsdk:"source"`
	Editable               types.Bool   `tfsdk:"editable"`
	RestartRequired        types.Bool   `tfsdk:"restart_required"`
}

func (r *OIDCProviderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_oidc_provider"
}

func (r *OIDCProviderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a RustFS OIDC identity provider",
		MarkdownDescription: "Manage one persisted OIDC identity provider used by RustFS Console " +
			"single sign-on (`GET`/`PUT`/`DELETE /rustfs/admin/v3/oidc/config`).\n\n" +
			"Creating or updating the resource first runs OIDC discovery through " +
			"`POST /rustfs/admin/v3/oidc/validate`, so an unreachable `config_url` or an invalid " +
			"discovery document fails the apply before anything is persisted.\n\n" +
			"**Lockout risk:** deleting the last enabled provider can lock operators out of the " +
			"RustFS Console. The provider emits a warning when it detects that condition.\n\n" +
			"**Secrets:** the server never returns `client_secret`. The provider keeps the configured " +
			"value in state (marked sensitive) while the server reports the secret as configured, and " +
			"removing it from the configuration does not clear it on the server. Destroy and recreate " +
			"the provider to remove its secret.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:    true,
				Description: "Provider id (alphanumeric, underscore or hyphen). Changing this forces recreation.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(oidcProviderIDPattern, "must contain only letters, digits, underscores or hyphens"),
					stringvalidator.LengthAtLeast(1),
				},
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the provider is enabled for Console login. Defaults to `true`.",
			},
			"display_name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name shown on the Console login page. Defaults to the provider id.",
				PlanModifiers: []planmodifier.String{
					oidcDisplayNameDefault{},
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"config_url": schema.StringAttribute{
				Required:    true,
				Description: "OIDC discovery document URL, for example `https://kc.example.com/realms/rustfs/.well-known/openid-configuration`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"issuer": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Issuer expected in the discovery document. Null lets the server derive and verify it from `config_url`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"client_id": schema.StringAttribute{
				Required:    true,
				Description: "OAuth client id registered with the identity provider.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"client_secret": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "OAuth client secret. The server never returns it; the configured value is kept in state while the server reports a secret is configured.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"client_secret_configured": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the server holds a client secret for this provider.",
			},
			"scopes": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default: listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{
					types.StringValue("openid"),
					types.StringValue("profile"),
					types.StringValue("email"),
				})),
				Description: "OAuth scopes. Must include `openid`. Defaults to `[\"openid\", \"profile\", \"email\"]`.",
			},
			"other_audiences": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{})),
				Description: "Additional accepted token audiences. Defaults to an empty list.",
			},
			"redirect_uri": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Callback URL registered with the identity provider. Required when `redirect_uri_dynamic` is `false`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"redirect_uri_dynamic": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the server derives the callback URL from the request. Defaults to `true`.",
			},
			"claim_name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("groups"),
				Description: "Claim that carries the user's policy groups. Defaults to `groups`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"claim_prefix": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
				Description: "Prefix stripped from policy claims. Defaults to an empty string.",
			},
			"role_policy": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
				Description: "Policy applied to every authenticated user. Defaults to an empty string.",
			},
			"groups_claim": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("groups"),
				Description: "Claim that carries group names. Defaults to `groups`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"roles_claim": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
				Description: "Claim that carries role names. Defaults to an empty string.",
			},
			"email_claim": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("email"),
				Description: "Claim that carries the user's email address. Defaults to `email`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"username_claim": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("preferred_username"),
				Description: "Claim that carries the user name. Defaults to `preferred_username`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"hide_from_ui": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Hide the provider from the Console login page. Defaults to `false`.",
			},
			"source": schema.StringAttribute{
				Computed:    true,
				Description: "Where the effective configuration comes from: `persisted` (managed here) or `env` (server environment variables).",
			},
			"editable": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the server accepts changes for this provider. Environment-managed providers are not editable.",
			},
			"restart_required": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether persisted changes differ from the configuration the running server loaded and a restart is required.",
			},
		},
	}
}

func (r *OIDCProviderResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *OIDCProviderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan OIDCProviderResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := plan.ID.ValueString()
	input, diags := oidcProviderInputFromModel(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Validate before persisting: the server stores a provider without running
	// discovery, so without this check a typo in config_url would only surface
	// at Console login.
	if _, err := r.client.RustClient.ValidateOIDCProvider(ctx, rustfs.OIDCProviderValidationInput{
		ProviderID:        id,
		OIDCProviderInput: input,
	}); err != nil {
		resp.Diagnostics.AddError(
			"Error validating OIDC provider",
			"Could not validate OIDC provider "+id+": "+err.Error(),
		)
		return
	}

	if err := r.client.RustClient.UpsertOIDCProvider(ctx, id, input); err != nil {
		resp.Diagnostics.AddError(
			"Error creating OIDC provider",
			"Could not create OIDC provider "+id+": "+err.Error(),
		)
		return
	}

	config, err := r.client.RustClient.ListOIDCConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating OIDC provider",
			"OIDC provider "+id+" was saved, but reading it back failed: "+err.Error(),
		)
		return
	}
	provider, found := findOIDCProvider(config, id)
	if !found {
		resp.Diagnostics.AddError(
			"Error creating OIDC provider",
			"OIDC provider "+id+" was saved, but the server did not return it.",
		)
		return
	}

	applyOIDCProvider(&plan, provider, oidcProviderSecret(provider.ClientSecretConfigured, plan.ClientSecret))
	plan.RestartRequired = types.BoolValue(config.RestartRequired)
	tflog.Trace(ctx, "created an OIDC provider", map[string]any{"provider_id": id})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *OIDCProviderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state OIDCProviderResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	config, err := r.client.RustClient.ListOIDCConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading OIDC provider",
			"Could not read OIDC provider "+id+": "+err.Error(),
		)
		return
	}

	for _, provider := range config.Providers {
		if provider.ProviderID != id {
			continue
		}
		applyOIDCProvider(&state, provider, oidcProviderSecret(provider.ClientSecretConfigured, state.ClientSecret))
		state.RestartRequired = types.BoolValue(config.RestartRequired)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	tflog.Trace(ctx, "OIDC provider no longer exists", map[string]any{"provider_id": id})
	resp.State.RemoveResource(ctx)
}

func (r *OIDCProviderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan OIDCProviderResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := plan.ID.ValueString()
	input, diags := oidcProviderInputFromModel(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.RustClient.ValidateOIDCProvider(ctx, rustfs.OIDCProviderValidationInput{
		ProviderID:        id,
		OIDCProviderInput: input,
	}); err != nil {
		resp.Diagnostics.AddError(
			"Error validating OIDC provider",
			"Could not validate OIDC provider "+id+": "+err.Error(),
		)
		return
	}

	if err := r.client.RustClient.UpsertOIDCProvider(ctx, id, input); err != nil {
		resp.Diagnostics.AddError(
			"Error updating OIDC provider",
			"Could not update OIDC provider "+id+": "+err.Error(),
		)
		return
	}

	config, err := r.client.RustClient.ListOIDCConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error updating OIDC provider",
			"OIDC provider "+id+" was updated, but reading it back failed: "+err.Error(),
		)
		return
	}
	provider, found := findOIDCProvider(config, id)
	if !found {
		resp.Diagnostics.AddError(
			"Error updating OIDC provider",
			"OIDC provider "+id+" was updated, but the server did not return it.",
		)
		return
	}

	applyOIDCProvider(&plan, provider, oidcProviderSecret(provider.ClientSecretConfigured, plan.ClientSecret))
	plan.RestartRequired = types.BoolValue(config.RestartRequired)
	tflog.Trace(ctx, "updated an OIDC provider", map[string]any{"provider_id": id})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *OIDCProviderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state OIDCProviderResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	warnIfLastOIDCProvider(ctx, r.client.RustClient, id, &resp.Diagnostics)

	if err := r.client.RustClient.DeleteOIDCProvider(ctx, id); err != nil {
		// An out-of-band delete has nothing left to remove.
		if oidcProviderDeleteGone(err) {
			tflog.Trace(ctx, "OIDC provider was already deleted", map[string]any{"provider_id": id})
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting OIDC provider",
			"Could not delete OIDC provider "+id+": "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "deleted an OIDC provider", map[string]any{"provider_id": id})
}

func (r *OIDCProviderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// oidcProviderInputFromModel maps the plan to the server's request payload.
// Unknown computed values are sent as their server default (empty), and the
// read-back in Create/Update replaces them with the values the server stored.
func oidcProviderInputFromModel(ctx context.Context, model *OIDCProviderResourceModel) (rustfs.OIDCProviderInput, diag.Diagnostics) {
	scopes, diags := oidcStringSliceFromList(ctx, model.Scopes)
	otherAudiences, audienceDiags := oidcStringSliceFromList(ctx, model.OtherAudiences)
	diags.Append(audienceDiags...)

	return rustfs.OIDCProviderInput{
		Enabled:            oidcBool(model.Enabled, true),
		DisplayName:        oidcString(model.DisplayName),
		ConfigURL:          oidcString(model.ConfigURL),
		Issuer:             oidcOptionalString(model.Issuer),
		ClientID:           oidcString(model.ClientID),
		ClientSecret:       oidcOptionalString(model.ClientSecret),
		Scopes:             scopes,
		OtherAudiences:     otherAudiences,
		RedirectURI:        oidcOptionalString(model.RedirectURI),
		RedirectURIDynamic: oidcBool(model.RedirectURIDynamic, true),
		ClaimName:          oidcString(model.ClaimName),
		ClaimPrefix:        oidcString(model.ClaimPrefix),
		RolePolicy:         oidcString(model.RolePolicy),
		GroupsClaim:        oidcString(model.GroupsClaim),
		RolesClaim:         oidcString(model.RolesClaim),
		EmailClaim:         oidcString(model.EmailClaim),
		UsernameClaim:      oidcString(model.UsernameClaim),
		HideFromUI:         oidcBool(model.HideFromUI, false),
	}, diags
}

// oidcDisplayNameDefault fills an omitted display_name with the provider id.
// The server applies the same default, but doing it during planning keeps the
// documented default visible and avoids an unknown value after apply.
type oidcDisplayNameDefault struct{}

func (oidcDisplayNameDefault) Description(context.Context) string {
	return "defaults to the provider id"
}

func (oidcDisplayNameDefault) MarkdownDescription(context.Context) string {
	return "defaults to the provider id"
}

func (oidcDisplayNameDefault) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if !req.ConfigValue.IsNull() && !req.ConfigValue.IsUnknown() && strings.TrimSpace(req.ConfigValue.ValueString()) != "" {
		return
	}

	var id types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() || id.IsNull() || id.IsUnknown() {
		return
	}
	resp.PlanValue = id
}

// findOIDCProvider returns the provider with the given id from an effective
// configuration snapshot.
func findOIDCProvider(config rustfs.OIDCConfig, id string) (rustfs.OIDCProvider, bool) {
	for _, provider := range config.Providers {
		if provider.ProviderID == id {
			return provider, true
		}
	}
	return rustfs.OIDCProvider{}, false
}

// applyOIDCProvider copies the server's view of one provider into the model.
// secret is the value the resource decided to keep: the plan's value on
// create/update, or the prior state's value on read.
func applyOIDCProvider(model *OIDCProviderResourceModel, provider rustfs.OIDCProvider, secret types.String) {
	model.ID = types.StringValue(provider.ProviderID)
	model.Enabled = types.BoolValue(provider.Enabled)
	// The plan fills an omitted display_name with the provider id. Mirror that
	// here so an older server that stores an empty value cannot produce an
	// inconsistent post-apply result.
	displayName := provider.DisplayName
	if strings.TrimSpace(displayName) == "" {
		displayName = provider.ProviderID
	}
	model.DisplayName = types.StringValue(displayName)
	model.ConfigURL = types.StringValue(provider.ConfigURL)
	model.Issuer = stringFromPtr(provider.Issuer)
	model.ClientID = types.StringValue(provider.ClientID)
	model.ClientSecret = secret
	model.ClientSecretConfigured = types.BoolValue(provider.ClientSecretConfigured)
	model.Scopes = oidcStringList(provider.Scopes)
	model.OtherAudiences = oidcStringList(provider.OtherAudiences)
	model.RedirectURI = stringFromPtr(provider.RedirectURI)
	model.RedirectURIDynamic = types.BoolValue(provider.RedirectURIDynamic)
	model.ClaimName = types.StringValue(provider.ClaimName)
	model.ClaimPrefix = types.StringValue(provider.ClaimPrefix)
	model.RolePolicy = types.StringValue(provider.RolePolicy)
	model.GroupsClaim = types.StringValue(provider.GroupsClaim)
	model.RolesClaim = types.StringValue(provider.RolesClaim)
	model.EmailClaim = types.StringValue(provider.EmailClaim)
	model.UsernameClaim = types.StringValue(provider.UsernameClaim)
	model.HideFromUI = types.BoolValue(provider.HideFromUI)
	model.Source = types.StringValue(provider.Source)
	model.Editable = types.BoolValue(provider.Editable)
}

// oidcProviderSecret keeps a configured secret in state when the server still
// holds one. The server never returns the value, so preserving the prior state
// is the only way to avoid a permanent diff.
func oidcProviderSecret(configured bool, prior types.String) types.String {
	if !configured || prior.IsNull() || prior.IsUnknown() || prior.ValueString() == "" {
		return types.StringNull()
	}
	return prior
}

// warnIfLastOIDCProvider warns before deleting the only enabled provider. The
// server does not report this condition itself, so the provider counts the
// effective list. Failure to read the list is not a reason to block the delete.
func warnIfLastOIDCProvider(ctx context.Context, client rustfs.RustfsAdmin, id string, diags *diag.Diagnostics) {
	config, err := client.ListOIDCConfig(ctx)
	if err != nil {
		return
	}

	enabled := 0
	found := false
	for _, provider := range config.Providers {
		if provider.Enabled {
			enabled++
		}
		if provider.ProviderID == id {
			found = provider.Enabled
		}
	}
	if !found || enabled != 1 {
		return
	}

	diags.AddWarning(
		"Deleting the last enabled OIDC provider",
		"OIDC provider "+id+" is the only enabled provider. Deleting it can lock operators out "+
			"of the RustFS Console; make sure another authentication path is available.",
	)
	tflog.Debug(ctx, "deleting the last enabled OIDC provider", map[string]any{"provider_id": id})
}

// oidcProviderDeleteGone reports the server's answer for a provider that is
// already absent. The route has no dedicated code (it answers 400
// InvalidRequest), so this matches its own wording only.
func oidcProviderDeleteGone(err error) bool {
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusBadRequest &&
		strings.EqualFold(strings.TrimSpace(apiErr.Message), "provider not found")
}

func oidcString(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return value.ValueString()
}

func oidcOptionalString(value types.String) *string {
	text := oidcString(value)
	if text == "" {
		return nil
	}
	return new(text)
}

func oidcBool(value types.Bool, fallback bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueBool()
}

func oidcStringSliceFromList(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []string
	diags := list.ElementsAs(ctx, &values, false)
	if diags.HasError() {
		return nil, diags
	}
	if values == nil {
		values = []string{}
	}
	return values, diags
}

func oidcStringList(values []string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elems = append(elems, types.StringValue(value))
	}
	return types.ListValueMust(types.StringType, elems)
}

func init() {
	registerResource(NewOIDCProviderResource)
}
