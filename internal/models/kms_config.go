package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type KmsConfigDataSourceModel struct {
	Backend         types.String `tfsdk:"backend"`
	CacheEnabled    types.Bool   `tfsdk:"cache_enabled"`
	CacheMaxKeys    types.Int64  `tfsdk:"cache_max_keys"`
	CacheTTLSeconds types.Int64  `tfsdk:"cache_ttl_seconds"`
	DefaultKeyID    types.String `tfsdk:"default_key_id"`
}
