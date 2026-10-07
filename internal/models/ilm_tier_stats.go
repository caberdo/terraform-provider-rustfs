package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type IlmTierStatsDataSourceModel struct {
	ID    types.String `tfsdk:"id"`
	Tiers types.List   `tfsdk:"tiers"`
}

type TierStatsModel struct {
	Name        types.String `tfsdk:"name"`
	NumObjects  types.Int64  `tfsdk:"num_objects"`
	NumVersions types.Int64  `tfsdk:"num_versions"`
	TotalSize   types.Int64  `tfsdk:"total_size"`
}
