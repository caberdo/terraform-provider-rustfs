// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ClusterSnapshotDataSource{}

type ClusterSnapshotDataSource struct {
	client *AllClient
}

type ClusterSnapshotDataSourceModel struct {
	ID   types.String `tfsdk:"id"`
	JSON types.String `tfsdk:"json"`
}

func NewClusterSnapshotDataSource() datasource.DataSource {
	return &ClusterSnapshotDataSource{}
}

func (d *ClusterSnapshotDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster_snapshot"
}

func (d *ClusterSnapshotDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Read-only cluster snapshot from the RustFS v4 admin API " +
		"(GET /rustfs/admin/v4/cluster/snapshot). The snapshot is a large, deployment-shaped payload and " +
		"is returned as raw JSON so the provider follows the server instead of pinning every field. The " +
		"v4 admin API is a preview surface; a server that predates it answers `511 Upgrade Required`."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON cluster snapshot as returned by the server.",
			},
		},
	}
}

func (d *ClusterSnapshotDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *ClusterSnapshotDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	snapshot, err := d.client.RustClient.GetClusterSnapshot(ctx)
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"Cluster snapshot is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that serves the v4 admin API, or remove the "+
					"rustfs_cluster_snapshot data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading cluster snapshot",
			"Could not read cluster snapshot, unexpected error: "+err.Error(),
		)
		return
	}

	state := ClusterSnapshotDataSourceModel{
		ID:   types.StringValue("cluster-snapshot"),
		JSON: types.StringValue(snapshot),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func init() {
	registerDataSource(NewClusterSnapshotDataSource)
}
