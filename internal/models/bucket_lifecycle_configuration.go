package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketLifecycleConfigurationModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Id     types.String `tfsdk:"id"`
	Rule   []RuleModel  `tfsdk:"rule"`
}

type RuleModel struct {
	Id                             types.String                         `tfsdk:"id"`
	Status                         types.String                         `tfsdk:"status"`
	Filter                         *FilterModel                         `tfsdk:"filter"`
	Expiration                     *ExpirationModel                     `tfsdk:"expiration"`
	Transition                     *TransitionModel                     `tfsdk:"transition"`
	NoncurrentVersionExpiration    *NoncurrentVersionExpirationModel    `tfsdk:"noncurrent_version_expiration"`
	NoncurrentVersionTransition    *NoncurrentVersionTransitionModel    `tfsdk:"noncurrent_version_transition"`
	AbortIncompleteMultipartUpload *AbortIncompleteMultipartUploadModel `tfsdk:"abort_incomplete_multipart_upload"`
}

type FilterModel struct {
	Prefix types.String `tfsdk:"prefix"`
}

type ExpirationModel struct {
	Days                      types.Int64  `tfsdk:"days"`
	Date                      types.String `tfsdk:"date"`
	ExpiredObjectDeleteMarker types.Bool   `tfsdk:"expired_object_delete_marker"`
}

type TransitionModel struct {
	Days         types.Int64  `tfsdk:"days"`
	Date         types.String `tfsdk:"date"`
	StorageClass types.String `tfsdk:"storage_class"`
}

type NoncurrentVersionExpirationModel struct {
	NoncurrentDays types.Int64 `tfsdk:"noncurrent_days"`
}

type NoncurrentVersionTransitionModel struct {
	NoncurrentDays types.Int64  `tfsdk:"noncurrent_days"`
	StorageClass   types.String `tfsdk:"storage_class"`
}

type AbortIncompleteMultipartUploadModel struct {
	DaysAfterInitiation types.Int64 `tfsdk:"days_after_initiation"`
}
