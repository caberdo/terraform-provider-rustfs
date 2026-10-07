package models

import "github.com/hashicorp/terraform-plugin-framework/types"

// Data models.
type PolicyStatementModel struct {
	Effect   string   `tfsdk:"effect"`
	Action   []string `tfsdk:"action"`
	Resource []string `tfsdk:"resource"`
}

type PolicyResourceModel struct {
	Name      types.String           `tfsdk:"name"`
	Version   types.String           `tfsdk:"version"`
	Statement []PolicyStatementModel `tfsdk:"statement"`
}
