package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type ModuleSwitchRessourceModel struct {
	ID            types.String `tfsdk:"id"`
	NotifyEnabled types.Bool   `tfsdk:"notify_enabled"`
	AuditEnabled  types.Bool   `tfsdk:"audit_enabled"`
}
