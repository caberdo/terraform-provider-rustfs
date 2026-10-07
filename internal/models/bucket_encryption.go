package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketEncryptionResourceModel struct {
	Bucket         types.String `tfsdk:"bucket"`
	Algorithm      types.String `tfsdk:"algorithm"`
	KmsMasterKeyID types.String `tfsdk:"kms_master_key_id"`
}
