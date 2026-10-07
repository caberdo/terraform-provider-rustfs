package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketDurabilityRessourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Mode   types.String `tfsdk:"mode"`
}
