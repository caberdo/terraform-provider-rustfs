package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type PoolsDataSourceModel struct {
	Names types.List `tfsdk:"names"`
}
