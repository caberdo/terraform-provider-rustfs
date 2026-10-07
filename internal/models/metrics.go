package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type MetricsDataSourceModel struct {
	Metrics types.String `tfsdk:"metrics"`
}
