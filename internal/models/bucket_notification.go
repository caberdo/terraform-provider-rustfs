package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketNotificationQueueModel struct {
	Arn          types.String `tfsdk:"arn"`
	Events       types.Set    `tfsdk:"events"`
	FilterPrefix types.String `tfsdk:"filter_prefix"`
	FilterSuffix types.String `tfsdk:"filter_suffix"`
}

type BucketNotificationResourceModel struct {
	Bucket types.String                   `tfsdk:"bucket"`
	Queue  []BucketNotificationQueueModel `tfsdk:"queue"`
}
