// Copyright (c) 2026 the terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &bucketsDataSource{}

// NewBucketsDataSource lists every bucket the deployment holds.
func NewBucketsDataSource() datasource.DataSource {
	return &bucketsDataSource{}
}

type bucketsDataSource struct {
	client *AllClient
}

type bucketsDataSourceModel struct {
	Buckets types.List `tfsdk:"buckets"`
}

type bucketSummaryModel struct {
	Name         types.String `tfsdk:"name"`
	CreationDate types.String `tfsdk:"creation_date"`
}

var bucketSummaryAttrTypes = map[string]attr.Type{
	"name":          types.StringType,
	"creation_date": types.StringType,
}

func (d *bucketsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_buckets"
}

func (d *bucketsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List S3 buckets in rustfs",
		MarkdownDescription: "List every S3 bucket the RustFS deployment reports, sorted by name. " +
			"The list carries the name and creation time the server returns; it does not issue a request per bucket.",
		Attributes: map[string]schema.Attribute{
			"buckets": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Buckets sorted by name.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Name of the bucket.",
						},
						"creation_date": schema.StringAttribute{
							Computed:    true,
							Description: "Creation time the server reports for the bucket (RFC3339).",
						},
					},
				},
			},
		},
	}
}

func (d *bucketsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config bucketsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := d.client.S3.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing buckets",
			"Could not list buckets: "+err.Error()+
				". The credentials need s3:ListAllMyBuckets (or equivalent); an empty list is never returned for a "+
				"failed authorization.",
		)
		return
	}

	buckets := make([]bucketSummaryModel, 0, len(out.Buckets))
	for _, bucket := range out.Buckets {
		creationDate := types.StringNull()
		if bucket.CreationDate != nil {
			creationDate = types.StringValue(bucket.CreationDate.UTC().Format(time.RFC3339))
		}
		buckets = append(buckets, bucketSummaryModel{
			Name:         types.StringValue(aws.ToString(bucket.Name)),
			CreationDate: creationDate,
		})
	}

	// Sort in the provider, not in the server's order: a plan diff must mean a
	// real change.
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Name.ValueString() < buckets[j].Name.ValueString()
	})

	list, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: bucketSummaryAttrTypes}, buckets)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Buckets = list

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func init() {
	registerDataSource(NewBucketsDataSource)
}
