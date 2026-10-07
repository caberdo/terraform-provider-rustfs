package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type IAMPoliciesDataSourceModel struct {
	Policies types.List `tfsdk:"policies"`
}

type IamPolicySummaryModel struct {
	Name types.String `tfsdk:"name"`
}
