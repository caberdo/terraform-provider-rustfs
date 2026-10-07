package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketVersioningResourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Status types.String `tfsdk:"status"`
}
