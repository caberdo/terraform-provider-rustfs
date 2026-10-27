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
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ServiceAccountsDataSource{}

// ServiceAccountsDataSource lists the IAM service accounts the server reports
// (GET /rustfs/admin/v3/list-service-accounts).
type ServiceAccountsDataSource struct {
	client *AllClient
}

type ServiceAccountsDataSourceModel struct {
	ID       types.String `tfsdk:"id"`
	User     types.String `tfsdk:"user"`
	Accounts types.List   `tfsdk:"accounts"`
}

type serviceAccountListModel struct {
	AccessKey     types.String `tfsdk:"access_key"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	Expiration    types.String `tfsdk:"expiration"`
	AccountStatus types.String `tfsdk:"account_status"`
	ParentUser    types.String `tfsdk:"parent_user"`
	ImpliedPolicy types.Bool   `tfsdk:"implied_policy"`
	Policy        types.String `tfsdk:"policy"`
}

func NewServiceAccountsDataSource() datasource.DataSource {
	return &ServiceAccountsDataSource{}
}

func (d *ServiceAccountsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_accounts"
}

func (d *ServiceAccountsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List the IAM service accounts RustFS manages",
		MarkdownDescription: "List the IAM service accounts (`GET /rustfs/admin/v3/list-service-accounts`), " +
			"the long-lived API keys a parent user owns. This is a different surface from " +
			"`rustfs_temporary_accounts`: that data source lists STS temporary keys (the short-lived " +
			"credentials minted by AssumeRole), while this one lists the persisted IAM service accounts.\n\n" +
			"The listing never returns a secret key, and the provider blanks the field on every account it " +
			"receives, so no key material can reach Terraform state. Manage accounts with " +
			"`rustfs_serviceaccount`.\n\n" +
			"The route is served by RustFS 1.0.1 and later; a server that does not implement it fails the " +
			"read with a capability diagnostic naming the route.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"user": schema.StringAttribute{
				Optional: true,
				Description: "IAM user whose service accounts are listed. Omit it to list the calling " +
					"identity's accounts; naming another user is a cross-user listing and needs the " +
					"ListServiceAccounts admin action.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"accounts": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Service accounts sorted by access key.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"access_key": schema.StringAttribute{
							Computed: true,
							Description: "Access key identifier, which is the identifier `rustfs_serviceaccount` " +
								"manages. It is a public identifier, not a secret.",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Display name of the account.",
						},
						"description": schema.StringAttribute{
							Computed:    true,
							Description: "Description of the account. Null when the server reports none.",
						},
						"expiration": schema.StringAttribute{
							Computed:    true,
							Description: "Expiry of the account in RFC 3339, or null when the server reports none.",
						},
						"account_status": schema.StringAttribute{
							Computed:    true,
							Description: "Account status the server reports, for example `on` or `off`.",
						},
						"parent_user": schema.StringAttribute{
							Computed:    true,
							Description: "User the service account belongs to.",
						},
						"implied_policy": schema.BoolAttribute{
							Computed: true,
							Description: "Whether the account inherits the parent user's policy (`true`) or " +
								"carries its own in `policy` (`false`).",
						},
						"policy": schema.StringAttribute{
							Computed:    true,
							Description: "Effective policy document (JSON). Null when the account implies the parent policy.",
						},
					},
				},
			},
		},
	}
}

func (d *ServiceAccountsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ServiceAccountsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ServiceAccountsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	accounts, err := d.client.RustClient.ListServiceAccountsFor(ctx, config.User.ValueString())
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"Service account listing is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_service_accounts data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error listing service accounts",
			"Could not list RustFS service accounts: "+err.Error(),
		)
		return
	}

	slices.SortFunc(accounts, func(a, b rustfs.ServiceAccount) int {
		return strings.Compare(a.AccessKey, b.AccessKey)
	})
	models := make([]serviceAccountListModel, 0, len(accounts))
	for _, account := range accounts {
		models = append(models, serviceAccountListModel{
			AccessKey:     types.StringValue(account.AccessKey),
			Name:          optionalStringValue(account.Name),
			Description:   optionalStringValue(account.Description),
			Expiration:    optionalStringValue(account.Expiration),
			AccountStatus: optionalStringValue(account.AccountStatus),
			ParentUser:    optionalStringValue(account.ParentUser),
			ImpliedPolicy: types.BoolValue(account.ImpliedPolicy),
			Policy:        optionalStringValue(account.Policy),
		})
	}

	listed, diags := types.ListValueFrom(ctx, serviceAccountListObjectType(), models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	config.ID = types.StringValue("service-accounts")
	config.Accounts = listed
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func serviceAccountListObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"access_key":     types.StringType,
			"name":           types.StringType,
			"description":    types.StringType,
			"expiration":     types.StringType,
			"account_status": types.StringType,
			"parent_user":    types.StringType,
			"implied_policy": types.BoolType,
			"policy":         types.StringType,
		},
	}
}

func init() {
	registerDataSource(NewServiceAccountsDataSource)
}
