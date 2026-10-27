// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource                   = &IntegrityInventoryDataSource{}
	_ datasource.DataSourceWithValidateConfig = &IntegrityInventoryDataSource{}
)

const integrityInventoryDefaultLimit = 100

// IntegrityInventoryDataSource reads one page of a bucket's shard-integrity
// inventory.
type IntegrityInventoryDataSource struct {
	client *AllClient
}

type IntegrityInventoryDataSourceModel struct {
	ID                types.String `tfsdk:"id"`
	Bucket            types.String `tfsdk:"bucket"`
	Prefix            types.String `tfsdk:"prefix"`
	KeyMarker         types.String `tfsdk:"key_marker"`
	VersionMarker     types.String `tfsdk:"version_marker"`
	Limit             types.Int64  `tfsdk:"limit"`
	ObservedAt        types.String `tfsdk:"observed_at"`
	BucketIncarnation types.String `tfsdk:"bucket_incarnation"`
	Items             types.List   `tfsdk:"items"`
	IsTruncated       types.Bool   `tfsdk:"is_truncated"`
	NextKeyMarker     types.String `tfsdk:"next_key_marker"`
	NextVersionMarker types.String `tfsdk:"next_version_marker"`
}

var integrityInventoryItemAttrTypes = map[string]attr.Type{
	"key":                   types.StringType,
	"version_id":            types.StringType,
	"data_dir":              types.StringType,
	"size":                  types.Int64Type,
	"protection":            types.StringType,
	"checksums":             types.MapType{ElemType: types.StringType},
	"checksum_is_multipart": types.BoolType,
	"audit_unsupported":     types.StringType,
	"source_fingerprint":    types.StringType,
	"observation_error":     types.StringType,
}

func NewIntegrityInventoryDataSource() datasource.DataSource {
	return &IntegrityInventoryDataSource{}
}

func (d *IntegrityInventoryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_integrity_inventory"
}

func (d *IntegrityInventoryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "One page of a bucket's per-object shard-integrity inventory " +
		"(GET /rustfs/admin/v3/integrity/{bucket}/inventory).\n\n" +
		"The route lists object versions and reads each version's metadata; it does not read object payloads. " +
		"A page holds at most 100 versions, so a large bucket needs the returned `next_key_marker` and " +
		"`next_version_marker` to page through it. The checksums shown here are inventory evidence, not a " +
		"migration acceptance decision: a `rustfs_integrity_job` compares against them only when the item " +
		"supplies an independent `expected_sha256`."
	resp.Schema = schema.Schema{
		Description:         "Read one page of a RustFS data-integrity inventory",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier, derived from the bucket.",
			},
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Bucket whose inventory is read.",
			},
			"prefix": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Object key prefix to narrow the page. The empty default covers the whole bucket.",
			},
			"key_marker": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Key marker from a previous page's `next_key_marker`.",
			},
			"version_marker": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Version marker from a previous page's `next_version_marker`. It requires " +
					"`key_marker`; the server rejects a version marker on its own.",
			},
			"limit": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Maximum versions in the page. RustFS accepts 1 to 100; the default is 100.",
				Validators: []validator.Int64{
					int64validator.Between(1, integrityInventoryDefaultLimit),
				},
			},
			"observed_at": schema.StringAttribute{
				Computed:    true,
				Description: "Server time at which the inventory page was produced.",
			},
			"bucket_incarnation": schema.StringAttribute{
				Computed:    true,
				Description: "Incarnation UUID of the bucket the page was read against.",
			},
			"items": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Object versions in the page.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key": schema.StringAttribute{
							Computed:    true,
							Description: "Object key.",
						},
						"version_id": schema.StringAttribute{
							Computed:    true,
							Description: "S3 version selector, including the literal `null` for the null version slot.",
						},
						"data_dir": schema.StringAttribute{
							Computed:    true,
							Description: "Data directory UUID, when the version has one.",
						},
						"size": schema.Int64Attribute{
							Computed:    true,
							Description: "Object size in bytes.",
						},
						"protection": schema.StringAttribute{
							Computed: true,
							Description: "Shard-integrity protection: `unknown`, `legacy`, `independent_commitment`, " +
								"`invalid_declaration` or `no_payload`.",
						},
						"checksums": schema.MapAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "Checksums recorded for the version, grouped by algorithm.",
						},
						"checksum_is_multipart": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the recorded checksum is multipart.",
						},
						"audit_unsupported": schema.StringAttribute{
							Computed: true,
							Description: "Why the version cannot be audited, for example `delete_marker`, " +
								"`transitioned_object`, `transformed_object`, `multipart_object` or " +
								"`invalid_protection_declaration`.",
						},
						"source_fingerprint": schema.StringAttribute{
							Computed:    true,
							Description: "Fingerprint of the version's metadata.",
						},
						"observation_error": schema.StringAttribute{
							Computed:    true,
							Description: "Why the version's metadata could not be observed.",
						},
					},
				},
			},
			"is_truncated": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether more versions follow this page.",
			},
			"next_key_marker": schema.StringAttribute{
				Computed:    true,
				Description: "Key marker for the next page, when `is_truncated` is true.",
			},
			"next_version_marker": schema.StringAttribute{
				Computed:    true,
				Description: "Version marker for the next page, when `is_truncated` is true.",
			},
		},
	}
}

func (d *IntegrityInventoryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *IntegrityInventoryDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config IntegrityInventoryDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.KeyMarker.IsUnknown() || config.VersionMarker.IsUnknown() {
		return
	}
	if config.VersionMarker.ValueString() != "" && config.KeyMarker.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("version_marker"),
			"Invalid integrity inventory request",
			"`version_marker` requires `key_marker`: RustFS rejects a version marker on its own. Set "+
				"`key_marker` from the previous page's `next_key_marker`.",
		)
	}
}

func (d *IntegrityInventoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config IntegrityInventoryDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := config.Bucket.ValueString()
	keyMarker := config.KeyMarker.ValueString()
	versionMarker := config.VersionMarker.ValueString()

	limit := integrityInventoryLimit(config.Limit)
	page, err := d.client.RustClient.IntegrityInventory(
		ctx, bucket,
		config.Prefix.ValueString(),
		keyMarker,
		versionMarker,
		limit,
	)
	if err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			resp.Diagnostics.AddError(
				"Data-integrity inventory is not available on this RustFS server",
				err.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_integrity_inventory data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading data-integrity inventory",
			"Could not read the inventory of bucket "+bucket+": "+err.Error(),
		)
		return
	}

	config.ID = types.StringValue("integrity-inventory-" + bucket)
	config.Limit = types.Int64Value(int64(limit))
	config.ObservedAt = types.StringValue(page.ObservedAt)
	config.BucketIncarnation = types.StringValue(page.BucketIncarnation)
	config.Items = integrityInventoryItemsValue(page.Items)
	config.IsTruncated = types.BoolValue(page.IsTruncated)
	config.NextKeyMarker = stringPointerValue(page.NextKeyMarker)
	config.NextVersionMarker = stringPointerValue(page.NextVersionMarker)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func integrityInventoryLimit(limit types.Int64) int {
	if limit.IsNull() || limit.IsUnknown() {
		return integrityInventoryDefaultLimit
	}
	return int(limit.ValueInt64())
}

func integrityInventoryItemsValue(items []rustfs.IntegrityInventoryItem) types.List {
	values := make([]attr.Value, 0, len(items))
	for _, item := range items {
		checksums := make(map[string]attr.Value, len(item.Checksums))
		for algorithm, value := range item.Checksums {
			checksums[algorithm] = types.StringValue(value)
		}
		values = append(values, types.ObjectValueMust(integrityInventoryItemAttrTypes, map[string]attr.Value{
			"key":                   types.StringValue(item.Key),
			"version_id":            types.StringValue(item.VersionID),
			"data_dir":              stringPointerValue(item.DataDir),
			"size":                  types.Int64Value(item.Size),
			"protection":            types.StringValue(item.Protection),
			"checksums":             types.MapValueMust(types.StringType, checksums),
			"checksum_is_multipart": types.BoolValue(item.ChecksumIsMultipart),
			"audit_unsupported":     stringPointerValue(item.AuditUnsupported),
			"source_fingerprint":    types.StringValue(item.SourceFingerprint),
			"observation_error":     stringPointerValue(item.ObservationError),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: integrityInventoryItemAttrTypes}, values)
}

func init() {
	registerDataSource(NewIntegrityInventoryDataSource)
}
