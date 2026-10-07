package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type IamBackupImportResourceModel struct {
	ContentBase64 types.String `tfsdk:"content_base64"`
}
