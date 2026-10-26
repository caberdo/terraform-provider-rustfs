// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &bucketAccelerateResource{}
	_ resource.ResourceWithImportState = &bucketAccelerateResource{}
)

// NewBucketAccelerateResource manages the S3 transfer-acceleration state of a
// bucket.
func NewBucketAccelerateResource() resource.Resource {
	return &bucketAccelerateResource{}
}

type bucketAccelerateResource struct {
	client *AllClient
}

type bucketAccelerateModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Id     types.String `tfsdk:"id"`
	Status types.String `tfsdk:"status"`
}

func (r *bucketAccelerateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_accelerate"
}

func (r *bucketAccelerateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage the accelerate configuration of an S3 bucket in rustfs",
		MarkdownDescription: "Manage the S3 transfer-acceleration status of a bucket. RustFS 1.0.x stores and " +
			"reports the value (`Enabled` or `Suspended`); destroying this resource sets `Suspended` because S3 has no " +
			"route that removes the configuration. The bucket itself is never deleted.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The bucket name",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Required:    true,
				Description: "Acceleration status: Enabled or Suspended.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.BucketAccelerateStatus("").Values())...),
				},
			},
		},
	}
}

func (r *bucketAccelerateResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	r.client = client
}

func (r *bucketAccelerateResource) put(ctx context.Context, bucket, status string) error {
	_, err := r.client.S3.PutBucketAccelerateConfiguration(ctx, &s3.PutBucketAccelerateConfigurationInput{
		Bucket: aws.String(bucket),
		AccelerateConfiguration: &s3types.AccelerateConfiguration{
			Status: s3types.BucketAccelerateStatus(status),
		},
	})
	return err
}

func (r *bucketAccelerateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketAccelerateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := plan.Bucket.ValueString()
	if err := r.put(ctx, bucket, plan.Status.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Error creating bucket accelerate configuration",
			"Could not set the accelerate status: "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "created a bucket accelerate configuration resource")

	plan.Id = types.StringValue(bucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketAccelerateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketAccelerateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.client.S3.GetBucketAccelerateConfiguration(ctx, &s3.GetBucketAccelerateConfigurationInput{
		Bucket: aws.String(state.Bucket.ValueString()),
	})
	if err != nil {
		if isBucketSubresourceAbsent(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket accelerate configuration",
			"Could not read the accelerate status: "+err.Error(),
		)
		return
	}

	// An empty status means the bucket has no accelerate configuration, which
	// is what the server reports for a bucket it never configured.
	if out.Status == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	state.Status = types.StringValue(string(out.Status))
	state.Id = types.StringValue(state.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketAccelerateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketAccelerateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := plan.Bucket.ValueString()
	if err := r.put(ctx, bucket, plan.Status.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket accelerate configuration",
			"Could not set the accelerate status: "+err.Error(),
		)
		return
	}

	plan.Id = types.StringValue(bucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketAccelerateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketAccelerateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// S3 has no route that removes the configuration, so destroying the
	// resource suspends acceleration, the documented "off" state.
	if err := r.put(ctx, data.Bucket.ValueString(), string(s3types.BucketAccelerateStatusSuspended)); err != nil {
		if isBucketSubresourceAbsent(err) {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket accelerate configuration",
			"Could not suspend the accelerate status: "+err.Error(),
		)
	}
}

func (r *bucketAccelerateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func init() {
	registerResource(NewBucketAccelerateResource)
}
