package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketResourceModel struct {
	Name types.String `tfsdk:"name"`
}
