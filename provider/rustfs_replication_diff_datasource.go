// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ReplicationDiffDataSource{}

type ReplicationDiffDataSource struct {
	client *AllClient
}

type ReplicationDiffDataSourceModel struct {
	Bucket                types.String `tfsdk:"bucket"`
	Prefix                types.String `tfsdk:"prefix"`
	ID                    types.String `tfsdk:"id"`
	Entries               types.List   `tfsdk:"entries"`
	IsTruncated           types.Bool   `tfsdk:"is_truncated"`
	ScannedVersions       types.Int64  `tfsdk:"scanned_versions"`
	ReplicationConfigured types.Bool   `tfsdk:"replication_configured"`
	JSON                  types.String `tfsdk:"json"`
}

var replicationDiffEntryAttrTypes = map[string]attr.Type{
	"object":             types.StringType,
	"version_id":         types.StringType,
	"size":               types.Int64Type,
	"delete_marker":      types.BoolType,
	"replication_status": types.StringType,
	"last_modified":      types.StringType,
}

func NewReplicationDiffDataSource() datasource.DataSource {
	return &ReplicationDiffDataSource{}
}

func (d *ReplicationDiffDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_replication_diff"
}

func (d *ReplicationDiffDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Reads the object versions of a bucket whose replication is still pending or has failed. " +
		"RustFS computes the diff on demand by scanning object versions, bounded to 10,000 versions per call; " +
		"`is_truncated` reports that the scan stopped early, so re-run with a narrower `prefix`. The bucket needs " +
		"a replication configuration. This data source is read-only: it never retries, resyncs or mutates " +
		"replication state. The counters are read from the aggregate response shape, a RustFS extension that " +
		"reports scan coverage; the madmin-compatible stream has no envelope to carry it."
	resp.Schema = schema.Schema{
		Description:         "Read the pending and failed replication diff of a bucket",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Bucket whose replication diff to read.",
			},
			"prefix": schema.StringAttribute{
				Optional: true,
				Description: "Only scan object versions under this prefix. Narrowing the prefix also narrows " +
					"the 10,000-version scan limit.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier, derived from the query.",
			},
			"entries": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Object versions with a pending or failed replication status.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"object": schema.StringAttribute{
							Computed:    true,
							Description: "Object key.",
						},
						"version_id": schema.StringAttribute{
							Computed:    true,
							Description: "Object version id; null when the listing did not report one.",
						},
						"size": schema.Int64Attribute{
							Computed:    true,
							Description: "Object version size in bytes.",
						},
						"delete_marker": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the version is a delete marker.",
						},
						"replication_status": schema.StringAttribute{
							Computed:    true,
							Description: "Replication status the server recorded, such as `PENDING` or `FAILED`.",
						},
						"last_modified": schema.StringAttribute{
							Computed:    true,
							Description: "Last modification time in RFC 3339; null when the server did not report one.",
						},
					},
				},
			},
			"is_truncated": schema.BoolAttribute{
				Computed: true,
				Description: "Whether the on-demand scan hit its 10,000-version limit before reaching the end of " +
					"the prefix, so `entries` is partial.",
			},
			"scanned_versions": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of object versions the server scanned for this diff.",
			},
			"replication_configured": schema.BoolAttribute{
				Computed: true,
				Description: "Whether the bucket has a replication configuration. When false the diff is empty " +
					"and no scan was run; the server reports ReplicationConfigurationNotFoundError.",
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON aggregate response as returned by the RustFS admin API.",
			},
		},
	}
}

func (d *ReplicationDiffDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ReplicationDiffDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ReplicationDiffDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := config.Bucket.ValueString()
	if bucket == "" {
		resp.Diagnostics.AddError(
			"Error reading replication diff",
			"bucket is required",
		)
		return
	}

	prefix := config.Prefix.ValueString()
	diff, err := d.client.RustClient.ReplicationDiff(ctx, bucket, prefix)
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"Replication diff is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_replication_diff data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading replication diff",
			"Could not read the replication diff for bucket "+bucket+": "+err.Error(),
		)
		return
	}

	config.ID = types.StringValue("replication-diff/" + bucket)
	if prefix != "" {
		config.ID = types.StringValue("replication-diff/" + bucket + "/" + prefix)
	}
	config.Entries = replicationDiffEntriesValue(diff.Entries)
	config.IsTruncated = types.BoolValue(diff.IsTruncated)
	config.ScannedVersions = types.Int64Value(diff.ScannedVersions)
	config.ReplicationConfigured = types.BoolValue(diff.ReplicationConfigured)
	if len(diff.Raw) > 0 {
		config.JSON = types.StringValue(string(diff.Raw))
	} else {
		config.JSON = types.StringValue("{}")
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func replicationDiffEntriesValue(entries []rustfs.ReplicationDiffEntry) types.List {
	values := make([]attr.Value, 0, len(entries))
	for _, entry := range entries {
		values = append(values, types.ObjectValueMust(replicationDiffEntryAttrTypes, map[string]attr.Value{
			"object":             types.StringValue(entry.Object),
			"version_id":         nullableStringPointer(entry.VersionID),
			"size":               types.Int64Value(entry.Size),
			"delete_marker":      types.BoolValue(entry.IsDeleteMarker),
			"replication_status": types.StringValue(entry.ReplicationStatus),
			"last_modified":      nullableStringPointer(entry.LastModified),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: replicationDiffEntryAttrTypes}, values)
}

func init() {
	registerDataSource(NewReplicationDiffDataSource)
}

// nullableStringPointer keeps an omitted optional string null instead of
// turning it into an empty value that would read as a real, empty id.
func nullableStringPointer(value *string) types.String {
	if value == nil || *value == "" {
		return types.StringNull()
	}
	return types.StringValue(*value)
}
