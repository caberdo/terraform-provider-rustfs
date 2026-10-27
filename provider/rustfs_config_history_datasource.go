// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ConfigHistoryDataSource{}

// ConfigHistoryDataSource lists the server's saved config snapshots from
// GET /rustfs/admin/v3/list-config-history-kv.
type ConfigHistoryDataSource struct {
	client *AllClient
}

type ConfigHistoryDataSourceModel struct {
	ID         types.String `tfsdk:"id"`
	MaxEntries types.Int64  `tfsdk:"max_entries"`
	Entries    types.List   `tfsdk:"entries"`
}

type ConfigHistoryEntryModel struct {
	RestoreID    types.String `tfsdk:"restore_id"`
	CreateTime   types.String `tfsdk:"create_time"`
	Data         types.String `tfsdk:"data"`
	DataRedacted types.Bool   `tfsdk:"data_redacted"`
}

var configHistoryEntryAttrTypes = map[string]attr.Type{
	"restore_id":    types.StringType,
	"create_time":   types.StringType,
	"data":          types.StringType,
	"data_redacted": types.BoolType,
}

func NewConfigHistoryDataSource() datasource.DataSource {
	return &ConfigHistoryDataSource{}
}

func (d *ConfigHistoryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config_history"
}

func (d *ConfigHistoryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Read the config snapshots RustFS stores before every configuration mutation " +
		"(GET /rustfs/admin/v3/list-config-history-kv). RustFS saves the whole server configuration, not a " +
		"per-subsystem diff, so every entry describes the same scopes; there is no per-subsystem filter and the " +
		"route records no actor. `data` is the snapshot in the same directive text as `get-config-kv` with " +
		"secret-like values replaced by the server (`data_redacted`), and it is marked sensitive because the " +
		"snapshot can still hold values the server does not classify as secrets. Pass `restore_id` to " +
		"`rustfs_config_restore` to roll back to an entry."
	resp.Schema = schema.Schema{
		Description:         "Read the stored config history",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"max_entries": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Description: "Maximum number of newest entries to return. The server requires this value; the " +
					"data source defaults to 100 and accepts 1..10000.",
				Validators: []validator.Int64{
					int64validator.Between(1, 10000),
				},
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"entries": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Stored snapshots, oldest first among the newest `max_entries` entries.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"restore_id": schema.StringAttribute{
							Computed:    true,
							Description: "Identifier to pass to `rustfs_config_restore`.",
						},
						"create_time": schema.StringAttribute{
							Computed:    true,
							Description: "When the server saved the snapshot, in RFC 3339.",
						},
						"data": schema.StringAttribute{
							Computed:    true,
							Sensitive:   true,
							Description: "Snapshot rendered as config directives with secret-like values redacted.",
						},
						"data_redacted": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the server redacted secret-like values in `data`.",
						},
					},
				},
			},
		},
	}
}

func (d *ConfigHistoryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ConfigHistoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ConfigHistoryDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	maxEntries := 100
	if !config.MaxEntries.IsNull() && !config.MaxEntries.IsUnknown() {
		maxEntries = int(config.MaxEntries.ValueInt64())
	}

	entries, err := d.client.RustClient.ListConfigHistory(ctx, maxEntries)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading config history",
			"Could not read the server's config history: "+err.Error(),
		)
		return
	}

	models := make([]ConfigHistoryEntryModel, 0, len(entries))
	for _, entry := range entries {
		models = append(models, ConfigHistoryEntryModel{
			RestoreID:    types.StringValue(entry.RestoreID),
			CreateTime:   types.StringValue(entry.CreateTime),
			Data:         types.StringValue(entry.Data),
			DataRedacted: types.BoolValue(entry.DataRedacted),
		})
	}
	list, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: configHistoryEntryAttrTypes}, models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	config.ID = types.StringValue(fmt.Sprintf("config-history/%d", maxEntries))
	config.MaxEntries = types.Int64Value(int64(maxEntries))
	config.Entries = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func init() {
	registerDataSource(NewConfigHistoryDataSource)
}
