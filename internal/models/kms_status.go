package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type KmsCacheStatsModel struct {
	HitCount      types.Int64 `tfsdk:"hit_count"`
	MissCount     types.Int64 `tfsdk:"miss_count"`
	EntryCount    types.Int64 `tfsdk:"entry_count"`
	EvictionCount types.Int64 `tfsdk:"eviction_count"`
}

type KmsClusterNodeModel struct {
	Host              types.String `tfsdk:"host"`
	ConfigFingerprint types.String `tfsdk:"config_fingerprint"`
	Error             types.String `tfsdk:"error"`
}

type KmsClusterConfigModel struct {
	Consistent types.Bool            `tfsdk:"consistent"`
	Nodes      []KmsClusterNodeModel `tfsdk:"nodes"`
}

type KmsStatusDataSourceModel struct {
	BackendType   types.String           `tfsdk:"backend_type"`
	BackendStatus types.String           `tfsdk:"backend_status"`
	CacheEnabled  types.Bool             `tfsdk:"cache_enabled"`
	CacheStats    *KmsCacheStatsModel    `tfsdk:"cache_stats"`
	DefaultKeyID  types.String           `tfsdk:"default_key_id"`
	Capabilities  types.Map              `tfsdk:"capabilities"`
	ClusterConfig *KmsClusterConfigModel `tfsdk:"cluster_config"`
}
