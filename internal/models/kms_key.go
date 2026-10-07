package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type KmsKeyRessourceModel struct {
	Name        types.String `tfsdk:"name"`
	KeyID       types.String `tfsdk:"key_id"`
	CreatedAt   types.String `tfsdk:"created_at"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	SkipDestroy types.Bool   `tfsdk:"skip_destroy"`
}
