package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type RustfsUserResourceModel struct {
	Name      types.String `tfsdk:"name"`
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
	Status    types.String `tfsdk:"status"`
	Policy    types.String `tfsdk:"policy"`
}
