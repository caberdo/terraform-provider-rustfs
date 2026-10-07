package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type LDAPPolicyAttachmentRessourceModel struct {
	UserOrGroup types.String `tfsdk:"user_or_group"`
	Policy      types.String `tfsdk:"policy"`
	IsGroup     types.Bool   `tfsdk:"is_group"`
	ID          types.String `tfsdk:"id"`
}
