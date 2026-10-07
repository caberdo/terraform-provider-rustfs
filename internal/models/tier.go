package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type TierResourceModel struct {
	Name       types.String `tfsdk:"name"`
	TierType   types.String `tfsdk:"tier_type"`
	ConfigJson types.String `tfsdk:"config_json"`
}
