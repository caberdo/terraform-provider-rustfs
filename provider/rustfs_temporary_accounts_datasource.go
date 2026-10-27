// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &TemporaryAccountsDataSource{}

type TemporaryAccountsDataSource struct {
	client *AllClient
}

type TemporaryAccountsDataSourceModel struct {
	UserProvider           types.String `tfsdk:"user_provider"`
	ParentUser             types.String `tfsdk:"parent_user"`
	IncludeServiceAccounts types.Bool   `tfsdk:"include_service_accounts"`
	IncludePolicies        types.Bool   `tfsdk:"include_policies"`
	Accounts               types.List   `tfsdk:"accounts"`
}

type temporaryAccountModel struct {
	AccessKey     types.String `tfsdk:"access_key"`
	ParentUser    types.String `tfsdk:"parent_user"`
	UserProvider  types.String `tfsdk:"user_provider"`
	Kind          types.String `tfsdk:"kind"`
	AccountStatus types.String `tfsdk:"account_status"`
	ImpliedPolicy types.Bool   `tfsdk:"implied_policy"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	Expiration    types.String `tfsdk:"expiration"`
	Policy        types.String `tfsdk:"policy"`
}

func NewTemporaryAccountsDataSource() datasource.DataSource {
	return &TemporaryAccountsDataSource{}
}

func (d *TemporaryAccountsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_temporary_accounts"
}

func (d *TemporaryAccountsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List RustFS temporary (STS) access keys",
		MarkdownDescription: "List the temporary (STS) access keys RustFS reports, optionally including service " +
			"accounts.\n\n" +
			"`rustfs_service_accounts` lists the IAM service accounts instead: those are the persisted, " +
			"long-lived API keys a parent user owns, while this data source lists STS temporary keys (set " +
			"`include_service_accounts` to add the service accounts to this listing).\n\n" +
			"RustFS has no route that lists every temporary account: `temporary-account-info` takes one access " +
			"key, so the listing comes from `list-access-keys-bulk`. The provider reads that route once per " +
			"identity provider to attribute each key: the `idp/ldap` and `idp/openid` routes only report keys " +
			"whose claims match, so a key the generic route reports and neither scoped route does is `builtin`.\n\n" +
			"The server enumerates IAM users, so an external identity that RustFS does not persist as an IAM " +
			"user is only listed when `parent_user` names it.\n\n" +
			"No secret key or session token is ever returned for a listed access key. Set `include_policies` to " +
			"read each key's effective policy as well; that costs one lookup per key.",
		Attributes: map[string]schema.Attribute{
			"user_provider": schema.StringAttribute{
				Optional: true,
				Description: "Only list keys for this identity provider: `builtin`, `ldap` or `openid` " +
					"(`openid` covers OIDC). Omit to list every provider.",
				Validators: []validator.String{
					stringvalidator.OneOf(
						rustfs.UserProviderBuiltin,
						rustfs.UserProviderLDAP,
						rustfs.UserProviderOpenID,
					),
				},
			},
			"parent_user": schema.StringAttribute{
				Optional: true,
				Description: "Only list keys owned by this parent user. Naming the user also reaches an " +
					"external identity that the server does not enumerate as an IAM user.",
			},
			"include_service_accounts": schema.BoolAttribute{
				Optional:    true,
				Description: "Also list long-lived service accounts stored beside the temporary keys. Omitted means false.",
			},
			"include_policies": schema.BoolAttribute{
				Optional: true,
				Description: "Read the effective policy, implied-policy flag and account status of every listed " +
					"key. Omitted means false. This costs one admin lookup per key and fails the read if a key " +
					"is revoked while it runs.",
			},
			"accounts": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Temporary access keys sorted by parent user, then access key.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"access_key": schema.StringAttribute{
							Computed:    true,
							Description: "Access key identifier. It is a public identifier, not a secret.",
						},
						"parent_user": schema.StringAttribute{
							Computed:    true,
							Description: "User the temporary credential was issued for.",
						},
						"user_provider": schema.StringAttribute{
							Computed:    true,
							Description: "Identity provider the server attributes the key to: `builtin`, `ldap` or `openid`.",
						},
						"kind": schema.StringAttribute{
							Computed:    true,
							Description: "`sts` for a temporary credential or `service-account` when `include_service_accounts` is set.",
						},
						"account_status": schema.StringAttribute{
							Computed: true,
							Description: "Account status the server reports, for example `on` or `off`. It is read " +
								"from the bulk listing unless `include_policies` is set, in which case the per-key " +
								"lookup supplies it. Null when the server reports none.",
						},
						"implied_policy": schema.BoolAttribute{
							Computed: true,
							Description: "Whether the key's effective policy is the parent user's implied policy " +
								"rather than an explicit session policy. Null unless `include_policies` is set: " +
								"the bulk listing does not report it.",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Optional display name of the key.",
						},
						"description": schema.StringAttribute{
							Computed:    true,
							Description: "Optional description of the key.",
						},
						"expiration": schema.StringAttribute{
							Computed:    true,
							Description: "Expiry of the credential in RFC 3339, or null when the server reports none.",
						},
						"policy": schema.StringAttribute{
							Computed:    true,
							Description: "Effective policy document (JSON) when `include_policies` is set, otherwise null.",
						},
					},
				},
			},
		},
	}
}

func (d *TemporaryAccountsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TemporaryAccountsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config TemporaryAccountsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	accounts, err := d.client.RustClient.ListTemporaryAccountsContext(ctx, rustfs.TemporaryAccountListOptions{
		UserProvider:           config.UserProvider.ValueString(),
		ParentUser:             config.ParentUser.ValueString(),
		IncludeServiceAccounts: config.IncludeServiceAccounts.ValueBool(),
	})
	if err != nil {
		d.addListError(resp, err)
		return
	}

	includePolicies := config.IncludePolicies.ValueBool()
	if includePolicies {
		if !d.enrichPolicies(ctx, accounts, resp) {
			return
		}
	}

	models := make([]temporaryAccountModel, 0, len(accounts))
	for _, account := range accounts {
		// The bulk listing hardcodes impliedPolicy=false, so without the
		// per-key lookup the field would be a misleading false. Null says
		// "not read" instead.
		impliedPolicy := types.BoolNull()
		if includePolicies {
			impliedPolicy = types.BoolValue(account.ImpliedPolicy)
		}

		models = append(models, temporaryAccountModel{
			AccessKey:     types.StringValue(account.AccessKey),
			ParentUser:    types.StringValue(account.ParentUser),
			UserProvider:  types.StringValue(account.UserProvider),
			Kind:          types.StringValue(account.Kind),
			AccountStatus: optionalStringValue(account.AccountStatus),
			ImpliedPolicy: impliedPolicy,
			Name:          optionalStringValue(account.Name),
			Description:   optionalStringValue(account.Description),
			Expiration:    optionalStringValue(account.Expiration),
			Policy:        optionalStringValue(account.Policy),
		})
	}

	listed, diags := types.ListValueFrom(ctx, temporaryAccountObjectType(), models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	config.Accounts = listed
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// accessKeyEnrichment is the per-key detail the bulk listing does not carry.
type accessKeyEnrichment struct {
	Policy        string
	ImpliedPolicy bool
	AccountStatus string
}

// enrichPolicies fills the per-key detail of every account in place. It reports
// false after writing a diagnostic, so the caller stops before setting state.
//
// The lookups are one per key and the provider's HTTP client has no overall
// timeout, so the loop observes the refresh context between keys and the client
// calls take it as well.
func (d *TemporaryAccountsDataSource) enrichPolicies(ctx context.Context, accounts []rustfs.TemporaryAccount, resp *datasource.ReadResponse) bool {
	for i := range accounts {
		if err := ctx.Err(); err != nil {
			d.addCancelledError(resp, err)
			return false
		}

		info, err := d.accountInfo(ctx, accounts[i])
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				d.addCancelledError(resp, ctxErr)
				return false
			}
			// A key revoked between the bulk listing and this lookup answers
			// 404 NoSuchResource, which the client wraps as an
			// UnsupportedFeatureError because it treats every 404 as a missing
			// route. Report the race instead of suggesting an upgrade.
			if rustfs.IsAPIErrorCode(err, "NoSuchResource") {
				resp.Diagnostics.AddError(
					"Temporary account disappeared during the read",
					fmt.Sprintf("Access key %q was revoked while include_policies was reading it; retry the read.",
						accounts[i].AccessKey),
				)
				return false
			}
			var unsupported *rustfs.UnsupportedFeatureError
			if errors.As(err, &unsupported) {
				resp.Diagnostics.AddError(
					"Temporary account information is not available on this RustFS server",
					unsupported.Error()+". Upgrade RustFS to a release that implements the route, or set "+
						"include_policies = false on the rustfs_temporary_accounts data source.",
				)
				return false
			}
			resp.Diagnostics.AddError(
				"Error reading temporary account information",
				fmt.Sprintf("Could not read the policy of access key %q: %s", accounts[i].AccessKey, err),
			)
			return false
		}
		accounts[i].Policy = info.Policy
		accounts[i].ImpliedPolicy = info.ImpliedPolicy
		accounts[i].AccountStatus = info.AccountStatus
	}
	return true
}

func (d *TemporaryAccountsDataSource) addCancelledError(resp *datasource.ReadResponse, err error) {
	resp.Diagnostics.AddError(
		"Temporary account enrichment cancelled",
		"Stopped reading per-key temporary account information: "+err.Error()+". Retry the read.",
	)
}

func (d *TemporaryAccountsDataSource) accountInfo(ctx context.Context, account rustfs.TemporaryAccount) (accessKeyEnrichment, error) {
	if account.Kind == rustfs.AccessKeyKindServiceAccount {
		info, err := d.client.RustClient.ReadServiceAccountContext(ctx, account.AccessKey)
		if err != nil {
			return accessKeyEnrichment{}, err
		}
		return accessKeyEnrichment{
			Policy:        info.Policy,
			ImpliedPolicy: info.ImpliedPolicy,
			AccountStatus: info.AccountStatus,
		}, nil
	}

	info, err := d.client.RustClient.GetTemporaryAccountInfoContext(ctx, account.AccessKey)
	if err != nil {
		return accessKeyEnrichment{}, err
	}
	return accessKeyEnrichment{
		Policy:        info.Policy,
		ImpliedPolicy: info.ImpliedPolicy,
		AccountStatus: info.AccountStatus,
	}, nil
}

func (d *TemporaryAccountsDataSource) addListError(resp *datasource.ReadResponse, err error) {
	var unsupported *rustfs.UnsupportedFeatureError
	if errors.As(err, &unsupported) {
		resp.Diagnostics.AddError(
			"Temporary account listing is not available on this RustFS server",
			unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
				"rustfs_temporary_accounts data source from the configuration.",
		)
		return
	}
	resp.Diagnostics.AddError(
		"Error listing temporary accounts",
		"Could not list temporary accounts: "+err.Error(),
	)
}

func temporaryAccountObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"access_key":     types.StringType,
			"parent_user":    types.StringType,
			"user_provider":  types.StringType,
			"kind":           types.StringType,
			"account_status": types.StringType,
			"implied_policy": types.BoolType,
			"name":           types.StringType,
			"description":    types.StringType,
			"expiration":     types.StringType,
			"policy":         types.StringType,
		},
	}
}

func optionalStringValue(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

func init() {
	registerDataSource(NewTemporaryAccountsDataSource)
}
