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

var _ datasource.DataSource = &PluginsDataSource{}

type PluginsDataSource struct {
	client *AllClient
}

type PluginsDataSourceModel struct {
	ID        types.String `tfsdk:"id"`
	Catalog   types.String `tfsdk:"catalog"`
	Instances types.String `tfsdk:"instances"`
}

func NewPluginsDataSource() datasource.DataSource {
	return &PluginsDataSource{}
}

func (d *PluginsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugins"
}

func (d *PluginsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Plugin catalog and instances from the RustFS v4 admin API " +
		"(GET /rustfs/admin/v4/plugins/catalog and GET /rustfs/admin/v4/plugins/instances). Both " +
		"payloads are returned as the server's JSON verbatim: the v4 plugin schema is new and stays " +
		"opaque so a newer server does not require a provider release. Secret values in instance " +
		"configuration are redacted by the server. The v4 admin API is a preview surface; a server " +
		"that predates it answers `511 Upgrade Required`."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"catalog": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON plugin catalog as returned by the server.",
			},
			"instances": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				Description: "Raw JSON plugin instance list as returned by the server. Marked sensitive " +
					"because instance configuration can carry credentials; RustFS redacts secret fields.",
			},
		},
	}
}

func (d *PluginsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PluginsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	catalog, err := d.client.RustClient.GetPluginCatalog(ctx)
	if err != nil {
		d.reportError(resp, err)
		return
	}
	instances, err := d.client.RustClient.ListPluginInstances(ctx)
	if err != nil {
		d.reportError(resp, err)
		return
	}

	state := PluginsDataSourceModel{
		ID:        types.StringValue("plugins"),
		Catalog:   types.StringValue(catalog),
		Instances: types.StringValue(instances),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *PluginsDataSource) reportError(resp *datasource.ReadResponse, err error) {
	var unsupported *rustfs.UnsupportedFeatureError
	if errors.As(err, &unsupported) {
		resp.Diagnostics.AddError(
			"Plugins are not available on this RustFS server",
			unsupported.Error()+". Upgrade RustFS to a release that serves the v4 admin API, or remove the "+
				"rustfs_plugins data source from the configuration.",
		)
		return
	}
	resp.Diagnostics.AddError(
		"Error reading plugins",
		"Could not read plugins, unexpected error: "+err.Error(),
	)
}

func init() {
	registerDataSource(NewPluginsDataSource)
}
