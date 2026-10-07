package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type ServiceAccountResourceModel struct {
	AccessKey     types.String `tfsdk:"access_key"`
	SecretKey     types.String `tfsdk:"secret_key"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	TargetUser    types.String `tfsdk:"user"`
	Expiration    types.String `tfsdk:"expiration"`
	Policy        types.String `tfsdk:"policy"`
	ImpliedPolicy types.Bool   `tfsdk:"implied_policy"`
}
