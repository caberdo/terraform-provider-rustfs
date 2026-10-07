package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type IamPolicyStatementDataSourceModel struct {
	Effect   types.String `tfsdk:"effect"`
	Action   types.Set    `tfsdk:"action"`
	Resource types.Set    `tfsdk:"resource"`
}

type IAMPolicyDataSourceModel struct {
	Name      types.String                        `tfsdk:"name"`
	Version   types.String                        `tfsdk:"version"`
	Statement []IamPolicyStatementDataSourceModel `tfsdk:"statement"`
}
