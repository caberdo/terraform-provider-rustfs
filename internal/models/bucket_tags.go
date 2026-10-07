package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketTagsModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Id     types.String `tfsdk:"id"`
	Tags   types.Map    `tfsdk:"tags"`
}
