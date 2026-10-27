// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &BucketUsageDataSource{}

// BucketUsageDataSource reads the scanner's per-bucket accounting from
// GET /rustfs/admin/v3/usage/{bucket}.
type BucketUsageDataSource struct {
	client *AllClient
}

type BucketUsageDataSourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Bucket             types.String `tfsdk:"bucket"`
	Prefix             types.String `tfsdk:"prefix"`
	MaxEntries         types.Int64  `tfsdk:"max_entries"`
	Size               types.Int64  `tfsdk:"size"`
	Objects            types.Int64  `tfsdk:"objects"`
	Versions           types.Int64  `tfsdk:"versions"`
	DeleteMarkers      types.Int64  `tfsdk:"delete_markers"`
	SubPrefixes        types.List   `tfsdk:"sub_prefixes"`
	Compacted          types.Bool   `tfsdk:"compacted"`
	SubPrefixesPartial types.Bool   `tfsdk:"sub_prefixes_partial"`
	Truncated          types.Bool   `tfsdk:"truncated"`
	SetsReporting      types.Int64  `tfsdk:"sets_reporting"`
	SetsTotal          types.Int64  `tfsdk:"sets_total"`
	LastUpdate         types.String `tfsdk:"last_update"`
	JSON               types.String `tfsdk:"json"`
}

type BucketUsageSubPrefixModel struct {
	Prefix        types.String `tfsdk:"prefix"`
	Size          types.Int64  `tfsdk:"size"`
	Objects       types.Int64  `tfsdk:"objects"`
	Versions      types.Int64  `tfsdk:"versions"`
	DeleteMarkers types.Int64  `tfsdk:"delete_markers"`
}

var bucketUsageSubPrefixAttrTypes = map[string]attr.Type{
	"prefix":         types.StringType,
	"size":           types.Int64Type,
	"objects":        types.Int64Type,
	"versions":       types.Int64Type,
	"delete_markers": types.Int64Type,
}

func NewBucketUsageDataSource() datasource.DataSource {
	return &BucketUsageDataSource{}
}

func (d *BucketUsageDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_usage"
}

func (d *BucketUsageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Read the scanner's usage accounting for one bucket or prefix (GET /rustfs/admin/v3/usage/{bucket}). " +
		"The numbers are not real-time: the scanner publishes them after a cycle, the endpoint caches a computed " +
		"answer for up to 30 seconds, and `sets_reporting` below `sets_total` means at least one erasure set could " +
		"not contribute, so the counters are a lower bound. A newly created bucket can report zeros until the first " +
		"cycle covers it. An unknown bucket fails the read instead of reporting zeros. The read also performs an " +
		"S3 `HeadBucket` existence check, so the provider credentials need `s3:ListBucket` (or equivalent) on the " +
		"bucket in addition to the admin permission that serves the usage route. This is the scanner's " +
		"eventually-consistent accounting, not the quota subsystem's view: use `rustfs_quota_stats` for the " +
		"configured quota limit, the quota subsystem's usage and the remaining headroom."
	resp.Schema = schema.Schema{
		Description:         "Read a bucket's (or prefix's) usage accounting",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket.",
			},
			"prefix": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Limit the answer to this prefix. An empty value (the default) reports the whole bucket. " +
					"The server trims leading and trailing slashes.",
			},
			"max_entries": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Description: "Maximum sub-prefix rows to return. The server defaults to 1000 and accepts 1..10000; " +
					"the largest rows remain when the breakdown is truncated.",
				Validators: []validator.Int64{
					int64validator.Between(1, 10000),
				},
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier, the bucket and prefix that were read.",
			},
			"size": schema.Int64Attribute{
				Computed:    true,
				Description: "Bytes accounted for the bucket or prefix.",
			},
			"objects": schema.Int64Attribute{
				Computed:    true,
				Description: "Current object count accounted for the bucket or prefix.",
			},
			"versions": schema.Int64Attribute{
				Computed:    true,
				Description: "Version count accounted for the bucket or prefix.",
			},
			"delete_markers": schema.Int64Attribute{
				Computed:    true,
				Description: "Delete-marker count accounted for the bucket or prefix.",
			},
			"sub_prefixes": schema.ListNestedAttribute{
				Computed:    true,
				Description: "One level of sub-prefix breakdown, largest first.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"prefix": schema.StringAttribute{
							Computed:    true,
							Description: "Sub-prefix path.",
						},
						"size": schema.Int64Attribute{
							Computed:    true,
							Description: "Bytes accounted under the sub-prefix.",
						},
						"objects": schema.Int64Attribute{
							Computed:    true,
							Description: "Current object count under the sub-prefix.",
						},
						"versions": schema.Int64Attribute{
							Computed:    true,
							Description: "Version count under the sub-prefix.",
						},
						"delete_markers": schema.Int64Attribute{
							Computed:    true,
							Description: "Delete-marker count under the sub-prefix.",
						},
					},
				},
			},
			"compacted": schema.BoolAttribute{
				Computed: true,
				Description: "Every reporting set had this prefix compacted: the aggregate is valid but the " +
					"sub-prefix breakdown is empty on disk.",
			},
			"sub_prefixes_partial": schema.BoolAttribute{
				Computed: true,
				Description: "At least one reporting set could not attribute its objects to a sub-prefix, so the " +
					"breakdown is incomplete while the aggregate stays valid.",
			},
			"truncated": schema.BoolAttribute{
				Computed:    true,
				Description: "The breakdown exceeded `max_entries`; only the largest rows are returned.",
			},
			"sets_reporting": schema.Int64Attribute{
				Computed:    true,
				Description: "Erasure sets whose usage cache held this bucket and prefix.",
			},
			"sets_total": schema.Int64Attribute{
				Computed:    true,
				Description: "Erasure sets the deployment has.",
			},
			"last_update": schema.StringAttribute{
				Computed: true,
				Description: "Newest scanner update across the reporting sets, in RFC 3339. Null when no set has " +
					"published usage for this bucket yet.",
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw server response, for fields this data source does not surface yet.",
			},
		},
	}
}

func (d *BucketUsageDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *BucketUsageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config BucketUsageDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := config.Bucket.ValueString()
	prefix := ""
	if !config.Prefix.IsNull() && !config.Prefix.IsUnknown() {
		prefix = config.Prefix.ValueString()
	}
	maxEntries := 1000
	if !config.MaxEntries.IsNull() && !config.MaxEntries.IsUnknown() {
		maxEntries = int(config.MaxEntries.ValueInt64())
	}

	usage, err := d.client.RustClient.GetBucketUsage(bucket, prefix, maxEntries)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading bucket usage",
			"Could not read usage for bucket "+bucket+": "+err.Error(),
		)
		return
	}

	// The usage route cannot tell a missing bucket from one the scanner has not
	// covered: both answer zeroed counters. Confirm existence every read, because
	// a scanner cache can also outlive a deleted bucket and still report usage.
	if err := d.confirmBucketExists(ctx, bucket); err != nil {
		resp.Diagnostics.AddError(
			"Error reading bucket usage",
			"Could not confirm bucket "+bucket+" exists: "+err.Error(),
		)
		return
	}

	config.ID = types.StringValue(bucketUsageID(bucket, prefix))
	config.Prefix = types.StringValue(prefix)
	config.MaxEntries = types.Int64Value(int64(maxEntries))
	config.Size = types.Int64Value(usage.Usage.Size)
	config.Objects = types.Int64Value(usage.Usage.Objects)
	config.Versions = types.Int64Value(usage.Usage.Versions)
	config.DeleteMarkers = types.Int64Value(usage.Usage.DeleteMarkers)
	config.Compacted = types.BoolValue(usage.Compacted)
	config.SubPrefixesPartial = types.BoolValue(usage.SubPrefixesPartial)
	config.Truncated = types.BoolValue(usage.Truncated)
	config.SetsReporting = types.Int64Value(int64(usage.SetsReporting))
	config.SetsTotal = types.Int64Value(int64(usage.SetsTotal))
	config.JSON = types.StringValue(usage.RawJSON)

	if usage.LastUpdateUnixSecs == nil {
		config.LastUpdate = types.StringNull()
	} else {
		config.LastUpdate = types.StringValue(time.Unix(*usage.LastUpdateUnixSecs, 0).UTC().Format(time.RFC3339))
	}

	subPrefixes := make([]BucketUsageSubPrefixModel, 0, len(usage.SubPrefixes))
	for _, entry := range usage.SubPrefixes {
		subPrefixes = append(subPrefixes, BucketUsageSubPrefixModel{
			Prefix:        types.StringValue(entry.Prefix),
			Size:          types.Int64Value(entry.Usage.Size),
			Objects:       types.Int64Value(entry.Usage.Objects),
			Versions:      types.Int64Value(entry.Usage.Versions),
			DeleteMarkers: types.Int64Value(entry.Usage.DeleteMarkers),
		})
	}
	list, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: bucketUsageSubPrefixAttrTypes}, subPrefixes)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.SubPrefixes = list

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// confirmBucketExists distinguishes "the scanner has not covered this bucket
// yet" from "the bucket does not exist". The usage route cannot tell them
// apart: both answer zeroed counters.
func (d *BucketUsageDataSource) confirmBucketExists(ctx context.Context, bucket string) error {
	_, err := d.client.S3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return nil
	}

	if isBucketNotFound(err) {
		return fmt.Errorf("bucket %q does not exist", bucket)
	}
	return err
}

func bucketUsageID(bucket, prefix string) string {
	if prefix == "" {
		return bucket
	}
	return bucket + "/" + prefix
}

func init() {
	registerDataSource(NewBucketUsageDataSource)
}
