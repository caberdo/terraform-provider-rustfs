package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketPolicyModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Id     types.String `tfsdk:"id"`
	Policy types.String `tfsdk:"policy"`
}
