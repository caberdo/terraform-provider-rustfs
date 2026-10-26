// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &bucketPolicyStatusDataSource{}

// NewBucketPolicyStatusDataSource answers whether the server considers a bucket
// public.
func NewBucketPolicyStatusDataSource() datasource.DataSource {
	return &bucketPolicyStatusDataSource{}
}

type bucketPolicyStatusDataSource struct {
	client *AllClient
}

type bucketPolicyStatusModel struct {
	Bucket   types.String `tfsdk:"bucket"`
	Id       types.String `tfsdk:"id"`
	IsPublic types.Bool   `tfsdk:"is_public"`
}

func (d *bucketPolicyStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_policy_status"
}

func (d *bucketPolicyStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Read whether an S3 bucket in rustfs is public",
		MarkdownDescription: "Read whether the server considers a bucket public (`GetBucketPolicyStatus`). " +
			"RustFS evaluates the bucket policy itself and is strict: on 1.0.1 through 1.0.3 it reports true only " +
			"for a statement that grants `s3:*` on the bucket to an explicit `{\"AWS\": [\"*\"]}` principal. A " +
			"public `s3:GetObject` policy, the string form `\"*\"` and canned ACLs all report false. Canned bucket " +
			"ACLs are accepted by RustFS but not stored, so they never make this value true. Read-only.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket to inspect.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The bucket name, which is the data source id.",
			},
			"is_public": schema.BoolAttribute{
				Computed: true,
				Description: "Whether the server reports the bucket as public. A bucket without a policy reports " +
					"false without an error.",
			},
		},
	}
}

func (d *bucketPolicyStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketPolicyStatusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config bucketPolicyStatusModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := config.Bucket.ValueString()
	out, err := d.client.S3.GetBucketPolicyStatus(ctx, &s3.GetBucketPolicyStatusInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		if isBucketNotFound(err) {
			resp.Diagnostics.AddError(
				"Error reading bucket policy status",
				fmt.Sprintf("Bucket %q does not exist.", bucket),
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket policy status",
			"Could not read the policy status: "+err.Error(),
		)
		return
	}

	config.Id = types.StringValue(bucket)
	config.IsPublic = types.BoolValue(out.PolicyStatus != nil && aws.ToBool(out.PolicyStatus.IsPublic))
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func init() {
	registerDataSource(NewBucketPolicyStatusDataSource)
}
