package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type StorageInfoDataSourceModel struct {
	Backend types.Object `tfsdk:"backend"`
	Disks   types.List   `tfsdk:"disks"`
	RawJSON types.String `tfsdk:"raw_json"`
}

type StorageDiskModel struct {
	DiskIndex    types.Int64  `tfsdk:"disk_index"`
	Endpoint     types.String `tfsdk:"endpoint"`
	AvailSpace   types.Int64  `tfsdk:"avail_space"`
	FreeInodes   types.Int64  `tfsdk:"free_inodes"`
	UsedInodes   types.Int64  `tfsdk:"used_inodes"`
	Healing      types.Bool   `tfsdk:"healing"`
	Local        types.Bool   `tfsdk:"local"`
	Path         types.String `tfsdk:"path"`
	PoolIndex    types.Int64  `tfsdk:"pool_index"`
	RuntimeState types.String `tfsdk:"runtime_state"`
	Scanning     types.Bool   `tfsdk:"scanning"`
	SetIndex     types.Int64  `tfsdk:"set_index"`
	State        types.String `tfsdk:"state"`
	TotalSpace   types.Int64  `tfsdk:"total_space"`
	UsedSpace    types.Int64  `tfsdk:"used_space"`
	UUID         types.String `tfsdk:"uuid"`
}
