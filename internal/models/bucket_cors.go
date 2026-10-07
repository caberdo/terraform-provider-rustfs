package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketCorsModel struct {
	Bucket types.String    `tfsdk:"bucket"`
	Id     types.String    `tfsdk:"id"`
	Rule   []CorsRuleModel `tfsdk:"rule"`
}

type CorsRuleModel struct {
	AllowedHeaders types.Set    `tfsdk:"allowed_headers"`
	AllowedMethods types.Set    `tfsdk:"allowed_methods"`
	AllowedOrigins types.Set    `tfsdk:"allowed_origins"`
	ExposeHeaders  types.Set    `tfsdk:"expose_headers"`
	MaxAgeSeconds  types.Int64  `tfsdk:"max_age_seconds"`
	Id             types.String `tfsdk:"id"`
}
