package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketObjectLockResourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Mode   types.String `tfsdk:"mode"`
	Days   types.Int64  `tfsdk:"days"`
	Years  types.Int64  `tfsdk:"years"`
}
