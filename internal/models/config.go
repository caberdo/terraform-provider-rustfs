package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type ConfigRessourceModel struct {
	SubSystem types.String `tfsdk:"sub_system"`
	Settings  types.Map    `tfsdk:"settings"`
	ID        types.String `tfsdk:"id"`
}
