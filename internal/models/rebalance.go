package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type RebalanceResourceModel struct {
	ID types.String `tfsdk:"id"`
}
