package models

import "github.com/hashicorp/terraform-plugin-framework/types"

// HealthInfoDataSourceModel describes the data source state.
type HealthInfoDataSourceModel struct {
	HealthInfo types.String `tfsdk:"health_info"`
	ObdInfo    types.String `tfsdk:"obd_info"`
	Version    types.String `tfsdk:"version"`
	Region     types.String `tfsdk:"region"`
	Timestamp  types.String `tfsdk:"timestamp"`
	Drives     types.List   `tfsdk:"drives"`
}

type HealthDriveModel struct {
	Endpoint       types.String `tfsdk:"endpoint"`
	DrivePath      types.String `tfsdk:"drive_path"`
	State          types.String `tfsdk:"state"`
	TotalSpace     types.Int64  `tfsdk:"total_space"`
	UsedSpace      types.Int64  `tfsdk:"used_space"`
	AvailableSpace types.Int64  `tfsdk:"available_space"`
}
