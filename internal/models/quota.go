package models

import "github.com/hashicorp/terraform-plugin-framework/types"

// QuotaDataSourceModel maps the data source schema data.
type QuotaDataSourceModel struct {
	Bucket    types.String `tfsdk:"bucket"`
	Quota     types.Int64  `tfsdk:"quota"`
	QuotaType types.String `tfsdk:"quota_type"`
}

type QuotaResourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Quota  types.Int64  `tfsdk:"quota"`
}
