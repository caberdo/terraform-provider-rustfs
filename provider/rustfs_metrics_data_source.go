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

var _ datasource.DataSource = &MetricsDataSource{}

type MetricsDataSource struct {
	client *AllClient
}

type MetricsDataSourceModel struct {
	Metrics types.String `tfsdk:"metrics"`
}

func NewMetricsDataSource() datasource.DataSource {
	return &MetricsDataSource{}
}

func (d *MetricsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_metrics"
}

func (d *MetricsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Metrics stream for the RustFS server. Requires a server that implements " +
		"`GET /rustfs/admin/v3/metrics`: RustFS 1.0.1 through 1.0.3 register the route but answer " +
		"`501 NotImplemented`, which Terraform reports as an unsupported capability naming the " +
		"server version and the route."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"metrics": schema.StringAttribute{
				Computed:            true,
				Description:         "Raw metrics stream from the /metrics endpoint",
				MarkdownDescription: "Raw metrics stream from the /metrics endpoint",
			},
		},
	}
}

func (d *MetricsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *MetricsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	raw, err := d.client.RustClient.GetMetrics(ctx)
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"Metrics are not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_metrics data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading metrics",
			"Could not read metrics, unexpected error: "+err.Error(),
		)
		return
	}
	state := MetricsDataSourceModel{
		Metrics: types.StringValue(raw),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func init() {
	registerDataSource(NewMetricsDataSource)
}
