package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type ServerInfoDataSourceModel struct {
	Mode              types.String `tfsdk:"mode"`
	DeploymentID      types.String `tfsdk:"deployment_id"`
	Region            types.String `tfsdk:"region"`
	BitrotSelftest    types.String `tfsdk:"bitrot_selftest"`
	BackendType       types.String `tfsdk:"backend_type"`
	OfflineDisks      types.Int64  `tfsdk:"offline_disks"`
	OnlineDisks       types.Int64  `tfsdk:"online_disks"`
	TotalDrivesPerSet types.List   `tfsdk:"total_drives_per_set"`
	TotalSets         types.List   `tfsdk:"total_sets"`
	BucketCount       types.Int64  `tfsdk:"bucket_count"`
	ObjectCount       types.Int64  `tfsdk:"object_count"`
	VersionCount      types.Int64  `tfsdk:"version_count"`
	DeleteMarkerCount types.Int64  `tfsdk:"delete_marker_count"`
	UsageSize         types.Int64  `tfsdk:"usage_size"`
	PoolCount         types.Int64  `tfsdk:"pool_count"`
	Pools             types.List   `tfsdk:"pools"`
	Servers           types.List   `tfsdk:"servers"`
	RawJSON           types.String `tfsdk:"raw_json"`
}

type ServerInfoPoolModel struct {
	PoolNumber         types.Int64 `tfsdk:"pool_number"`
	SetNumber          types.Int64 `tfsdk:"set_number"`
	ID                 types.Int64 `tfsdk:"id"`
	RawCapacity        types.Int64 `tfsdk:"raw_capacity"`
	RawUsage           types.Int64 `tfsdk:"raw_usage"`
	Usage              types.Int64 `tfsdk:"usage"`
	ObjectsCount       types.Int64 `tfsdk:"objects_count"`
	VersionsCount      types.Int64 `tfsdk:"versions_count"`
	DeleteMarkersCount types.Int64 `tfsdk:"delete_markers_count"`
	HealDisks          types.Int64 `tfsdk:"heal_disks"`
}

type ServerInfoDriveModel struct {
	Endpoint     types.String  `tfsdk:"endpoint"`
	Path         types.String  `tfsdk:"path"`
	State        types.String  `tfsdk:"state"`
	RuntimeState types.String  `tfsdk:"runtime_state"`
	Healing      types.Bool    `tfsdk:"healing"`
	Local        types.Bool    `tfsdk:"local"`
	UUID         types.String  `tfsdk:"uuid"`
	Totalspace   types.Int64   `tfsdk:"totalspace"`
	Usedspace    types.Int64   `tfsdk:"usedspace"`
	Availspace   types.Int64   `tfsdk:"availspace"`
	Utilization  types.Float64 `tfsdk:"utilization"`
}

type ServerInfoServerModel struct {
	Endpoint      types.String           `tfsdk:"endpoint"`
	State         types.String           `tfsdk:"state"`
	Version       types.String           `tfsdk:"version"`
	Uptime        types.Int64            `tfsdk:"uptime"`
	NumCPU        types.Int64            `tfsdk:"num_cpu"`
	MaxProcs      types.Int64            `tfsdk:"max_procs"`
	MemAlloc      types.Int64            `tfsdk:"mem_alloc"`
	MemTotalAlloc types.Int64            `tfsdk:"mem_total_alloc"`
	Drives        []ServerInfoDriveModel `tfsdk:"drives"`
}
