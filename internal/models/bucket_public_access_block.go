package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketPublicAccessBlockModel struct {
	Bucket                types.String `tfsdk:"bucket"`
	Id                    types.String `tfsdk:"id"`
	BlockPublicAcls       types.Bool   `tfsdk:"block_public_acls"`
	IgnorePublicAcls      types.Bool   `tfsdk:"ignore_public_acls"`
	BlockPublicPolicy     types.Bool   `tfsdk:"block_public_policy"`
	RestrictPublicBuckets types.Bool   `tfsdk:"restrict_public_buckets"`
}
