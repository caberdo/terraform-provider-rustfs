package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type UsersDataSourceModel struct {
	Bucket     types.String `tfsdk:"bucket"`
	AccessKeys types.List   `tfsdk:"access_keys"`
}
