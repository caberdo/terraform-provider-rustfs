package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type ReplicationMetricsDataSourceModel struct {
	Bucket                   types.String `tfsdk:"bucket"`
	ID                       types.String `tfsdk:"id"`
	ReplicationCount         types.Int64  `tfsdk:"replication_count"`
	CompletedReplicationSize types.Int64  `tfsdk:"completed_replication_size"`
	ReplicaCount             types.Int64  `tfsdk:"replica_count"`
	ReplicaSize              types.Int64  `tfsdk:"replica_size"`
	Failed                   types.Object `tfsdk:"failed"`
	Queued                   types.Object `tfsdk:"queued"`
	Targets                  types.List   `tfsdk:"targets"`
	JSON                     types.String `tfsdk:"json"`
}
