package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type LDAPServiceAccountResourceModel struct {
	AccessKey   types.String `tfsdk:"access_key"`
	SecretKey   types.String `tfsdk:"secret_key"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	User        types.String `tfsdk:"user"`
	Policy      types.String `tfsdk:"policy"`
}
