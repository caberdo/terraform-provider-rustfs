package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type GroupPolicyAttachmentResourceModel struct {
	Group  types.String `tfsdk:"group"`
	Policy types.String `tfsdk:"policy"`
	ID     types.String `tfsdk:"id"`
}
