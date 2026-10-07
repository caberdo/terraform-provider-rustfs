package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type GroupResourceModel struct {
	Name    types.String `tfsdk:"name"`
	Status  types.String `tfsdk:"status"`
	Members types.Set    `tfsdk:"members"`
}
