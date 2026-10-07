package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type RemoteTargetRessourceModel struct {
	Arn          types.String `tfsdk:"arn"`
	Type         types.String `tfsdk:"type"`
	Endpoint     types.String `tfsdk:"endpoint"`
	AccessKey    types.String `tfsdk:"access_key"`
	SecretKey    types.String `tfsdk:"secret_key"`
	Secure       types.Bool   `tfsdk:"secure"`
	Region       types.String `tfsdk:"region"`
	Path         types.String `tfsdk:"path"`
	Bucket       types.String `tfsdk:"bucket"`
	TargetBucket types.String `tfsdk:"target_bucket"`
}
