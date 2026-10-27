// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &OIDCConfigDataSource{}

// OIDCConfigDataSource reads the effective OIDC provider configuration without
// managing it.
type OIDCConfigDataSource struct {
	client *AllClient
}

type OIDCConfigDataSourceModel struct {
	ID              types.String `tfsdk:"id"`
	ProviderID      types.String `tfsdk:"provider_id"`
	Providers       types.List   `tfsdk:"providers"`
	RestartRequired types.Bool   `tfsdk:"restart_required"`
}

type OIDCProviderDataSourceModel struct {
	ProviderID             types.String `tfsdk:"provider_id"`
	Source                 types.String `tfsdk:"source"`
	Editable               types.Bool   `tfsdk:"editable"`
	Enabled                types.Bool   `tfsdk:"enabled"`
	DisplayName            types.String `tfsdk:"display_name"`
	ConfigURL              types.String `tfsdk:"config_url"`
	Issuer                 types.String `tfsdk:"issuer"`
	ClientID               types.String `tfsdk:"client_id"`
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
}

func NewOIDCConfigDataSource() datasource.DataSource {
	return &OIDCConfigDataSource{}
}

func (d *OIDCConfigDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_oidc_config"
}

func (d *OIDCConfigDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Read the effective RustFS OIDC provider configuration",
		MarkdownDescription: "Read every effective OIDC provider RustFS reports " +
			"(`GET /rustfs/admin/v3/oidc/config`), including providers configured through server " +
			"environment variables. Set `provider_id` to return only that provider.\n\n" +
			"The server never returns client secrets; `client_secret_configured` reports whether " +
			"one is stored.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier: `oidc-config` when unfiltered, otherwise the selected provider id.",
			},
			"provider_id": schema.StringAttribute{
				Optional:    true,
				Description: "Only return the provider with this id. When omitted, every effective provider is returned.",
			},
			"restart_required": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether persisted changes differ from the configuration the running server loaded and a restart is required.",
			},
			"providers": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Effective OIDC providers.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"provider_id": schema.StringAttribute{
							Computed:    true,
							Description: "Provider id.",
						},
						"source": schema.StringAttribute{
							Computed:    true,
							Description: "Where the configuration comes from: `persisted` or `env`.",
						},
						"editable": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the server accepts changes for the provider.",
						},
						"enabled": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the provider is enabled for Console login.",
						},
						"display_name": schema.StringAttribute{
							Computed:    true,
							Description: "Name shown on the Console login page.",
						},
						"config_url": schema.StringAttribute{
							Computed:    true,
							Description: "OIDC discovery document URL.",
						},
						"issuer": schema.StringAttribute{
							Computed:    true,
							Description: "Configured issuer. Null when the server derives it from `config_url`.",
						},
						"client_id": schema.StringAttribute{
							Computed:    true,
							Description: "OAuth client id.",
						},
						"client_secret_configured": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the server holds a client secret.",
						},
						"scopes": schema.ListAttribute{
							ElementType: types.StringType,
							Computed:    true,
							Description: "OAuth scopes.",
						},
						"other_audiences": schema.ListAttribute{
							ElementType: types.StringType,
							Computed:    true,
							Description: "Additional accepted token audiences.",
						},
						"redirect_uri": schema.StringAttribute{
							Computed:    true,
							Description: "Registered callback URL. Null when the server derives it dynamically.",
						},
						"redirect_uri_dynamic": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the server derives the callback URL from the request.",
						},
						"claim_name": schema.StringAttribute{
							Computed:    true,
							Description: "Claim that carries the user's policy groups.",
						},
						"claim_prefix": schema.StringAttribute{
							Computed:    true,
							Description: "Prefix stripped from policy claims.",
						},
						"role_policy": schema.StringAttribute{
							Computed:    true,
							Description: "Policy applied to every authenticated user.",
						},
						"groups_claim": schema.StringAttribute{
							Computed:    true,
							Description: "Claim that carries group names.",
						},
						"roles_claim": schema.StringAttribute{
							Computed:    true,
							Description: "Claim that carries role names.",
						},
						"email_claim": schema.StringAttribute{
							Computed:    true,
							Description: "Claim that carries the user's email address.",
						},
						"username_claim": schema.StringAttribute{
							Computed:    true,
							Description: "Claim that carries the user name.",
						},
						"hide_from_ui": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the provider is hidden from the Console login page.",
						},
					},
				},
			},
		},
	}
}

func (d *OIDCConfigDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *OIDCConfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config OIDCConfigDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	effective, err := d.client.RustClient.ListOIDCConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading OIDC config",
			"Could not read OIDC config: "+err.Error(),
		)
		return
	}

	filter := ""
	if !config.ProviderID.IsNull() && !config.ProviderID.IsUnknown() {
		filter = config.ProviderID.ValueString()
	}

	providers := make([]OIDCProviderDataSourceModel, 0, len(effective.Providers))
	for _, provider := range effective.Providers {
		if filter != "" && provider.ProviderID != filter {
			continue
		}
		providers = append(providers, OIDCProviderDataSourceModel{
			ProviderID:             types.StringValue(provider.ProviderID),
			Source:                 types.StringValue(provider.Source),
			Editable:               types.BoolValue(provider.Editable),
			Enabled:                types.BoolValue(provider.Enabled),
			DisplayName:            types.StringValue(provider.DisplayName),
			ConfigURL:              types.StringValue(provider.ConfigURL),
			Issuer:                 stringFromPtr(provider.Issuer),
			ClientID:               types.StringValue(provider.ClientID),
			ClientSecretConfigured: types.BoolValue(provider.ClientSecretConfigured),
			Scopes:                 oidcStringList(provider.Scopes),
			OtherAudiences:         oidcStringList(provider.OtherAudiences),
			RedirectURI:            stringFromPtr(provider.RedirectURI),
			RedirectURIDynamic:     types.BoolValue(provider.RedirectURIDynamic),
			ClaimName:              types.StringValue(provider.ClaimName),
			ClaimPrefix:            types.StringValue(provider.ClaimPrefix),
			RolePolicy:             types.StringValue(provider.RolePolicy),
			GroupsClaim:            types.StringValue(provider.GroupsClaim),
			RolesClaim:             types.StringValue(provider.RolesClaim),
			EmailClaim:             types.StringValue(provider.EmailClaim),
			UsernameClaim:          types.StringValue(provider.UsernameClaim),
			HideFromUI:             types.BoolValue(provider.HideFromUI),
		})
	}

	if filter != "" && len(providers) == 0 {
		resp.Diagnostics.AddError(
			"OIDC provider not found",
			"No OIDC provider with id "+filter+" exists on this server.",
		)
		return
	}

	providerType := types.ObjectType{AttrTypes: oidcProviderObjectAttrTypes()}
	providerList, diags := types.ListValueFrom(ctx, providerType, providers)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	config.ID = types.StringValue("oidc-config")
	if filter != "" {
		config.ID = types.StringValue(filter)
	}
	config.Providers = providerList
	config.RestartRequired = types.BoolValue(effective.RestartRequired)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func oidcProviderObjectAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"provider_id":              types.StringType,
		"source":                   types.StringType,
		"editable":                 types.BoolType,
		"enabled":                  types.BoolType,
		"display_name":             types.StringType,
		"config_url":               types.StringType,
		"issuer":                   types.StringType,
		"client_id":                types.StringType,
		"client_secret_configured": types.BoolType,
		"scopes":                   types.ListType{ElemType: types.StringType},
		"other_audiences":          types.ListType{ElemType: types.StringType},
		"redirect_uri":             types.StringType,
		"redirect_uri_dynamic":     types.BoolType,
		"claim_name":               types.StringType,
		"claim_prefix":             types.StringType,
		"role_policy":              types.StringType,
		"groups_claim":             types.StringType,
		"roles_claim":              types.StringType,
		"email_claim":              types.StringType,
		"username_claim":           types.StringType,
		"hide_from_ui":             types.BoolType,
	}
}

func init() {
	registerDataSource(NewOIDCConfigDataSource)
}
