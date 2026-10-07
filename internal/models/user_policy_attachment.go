package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type UserPolicyAttachmentRessourceModel struct {
	User   types.String `tfsdk:"user"`
	Policy types.String `tfsdk:"policy"`
	ID     types.String `tfsdk:"id"`
}
