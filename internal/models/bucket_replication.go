package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type BucketReplicationResourceModel struct {
	Bucket                  types.String `tfsdk:"bucket"`
	Role                    types.String `tfsdk:"role"`
	DestinationBucket       types.String `tfsdk:"destination_bucket"`
	Priority                types.Int64  `tfsdk:"priority"`
	Status                  types.String `tfsdk:"status"`
	DeleteMarkerReplication types.String `tfsdk:"delete_marker_replication"`
	DeleteReplication       types.String `tfsdk:"delete_replication"`
}
