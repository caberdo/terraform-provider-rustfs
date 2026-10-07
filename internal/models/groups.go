package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type GroupsDataSourceModel struct {
	Groups types.Set `tfsdk:"groups"`
}
