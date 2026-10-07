package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type UserMfaDataSourceModel struct {
	AccessKey              types.String `tfsdk:"access_key"`
	Enabled                types.Bool   `tfsdk:"enabled"`
	ActivatedAt            types.String `tfsdk:"activated_at"`
	RecoveryCodesRemaining types.Int64  `tfsdk:"recovery_codes_remaining"`
}
