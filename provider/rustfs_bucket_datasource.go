// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &bucketDataSource{}

// NewBucketDataSource is the data source counterpart of rustfs_bucket: it
// looks a bucket up instead of managing it.
func NewBucketDataSource() datasource.DataSource {
	return &bucketDataSource{}
}

type bucketDataSource struct {
	client *AllClient
}

type bucketDataSourceModel struct {
	Name         types.String `tfsdk:"name"`
	Id           types.String `tfsdk:"id"`
	Exists       types.Bool   `tfsdk:"exists"`
	CreationDate types.String `tfsdk:"creation_date"`
	Region       types.String `tfsdk:"region"`
}

func (d *bucketDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

func (d *bucketDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Look up an S3 bucket in rustfs",
		MarkdownDescription: "Look up one S3 bucket in rustfs: whether it exists, when it was created and " +
			"the region the server reports for it. Read-only; use the `rustfs_bucket` *resource* to manage a bucket.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket to look up.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The bucket name, which is the data source id.",
			},
			"exists": schema.BoolAttribute{
				Computed: true,
				Description: "Whether the bucket exists. A missing bucket reports false without an error, so the " +
					"value can drive count and for_each decisions.",
			},
			"creation_date": schema.StringAttribute{
				Computed:    true,
				Description: "Creation time the server reports for the bucket (RFC3339), empty when the bucket does not exist.",
			},
			"region": schema.StringAttribute{
				Computed: true,
				Description: "Region the server reports for the bucket, empty when the server reports none or does not " +
					"implement the location lookup. An empty region means the endpoint's default region.",
			},
		},
	}
}

func (d *bucketDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config bucketDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := config.Name.ValueString()
	config.Id = types.StringValue(name)

	exists, err := bucketExists(ctx, d.client.S3, name)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error looking up bucket",
			fmt.Sprintf("Could not check bucket %q: %s. The credentials need s3:ListBucket (or equivalent) on the bucket.",
				name, err),
		)
		return
	}

	config.Exists = types.BoolValue(exists)
	if !exists {
		config.CreationDate = types.StringNull()
		config.Region = types.StringNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
		return
	}

	out, err := d.client.S3.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing buckets",
			"Could not read the bucket's creation date: "+err.Error()+
				". The credentials need s3:ListAllMyBuckets (or equivalent).",
		)
		return
	}

	config.CreationDate = types.StringNull()
	for _, bucket := range out.Buckets {
		if aws.ToString(bucket.Name) == name && bucket.CreationDate != nil {
			config.CreationDate = types.StringValue(bucket.CreationDate.UTC().Format(time.RFC3339))
			break
		}
	}

	// A server that does not implement GetBucketLocation (or reports nothing)
	// leaves the region empty instead of failing the read.
	config.Region = types.StringValue("")
	if location, err := d.client.S3.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(name),
	}); err == nil {
		config.Region = types.StringValue(string(location.LocationConstraint))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func init() {
	registerDataSource(NewBucketDataSource)
}
