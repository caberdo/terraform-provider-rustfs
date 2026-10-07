package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketMetadataBackupDataSourceModel struct {
	ContentBase64 types.String `tfsdk:"content_base64"`
}
