package kms_status

import "github.com/hashicorp/terraform-plugin-framework/types"

func stringFromPtr(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}
