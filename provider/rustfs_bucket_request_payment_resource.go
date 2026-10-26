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
	_ resource.Resource                = &bucketRequestPaymentResource{}
	_ resource.ResourceWithImportState = &bucketRequestPaymentResource{}
)

// NewBucketRequestPaymentResource manages whether a bucket is requester-pays.
func NewBucketRequestPaymentResource() resource.Resource {
	return &bucketRequestPaymentResource{}
}

type bucketRequestPaymentResource struct {
	client *AllClient
}

type bucketRequestPaymentModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Id     types.String `tfsdk:"id"`
	Payer  types.String `tfsdk:"payer"`
}

func (r *bucketRequestPaymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_request_payment"
}

func (r *bucketRequestPaymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage the request-payment (requester pays) configuration of an S3 bucket in rustfs",
		MarkdownDescription: "Manage who pays for requests and data transfer on a bucket. `Requester` makes the bucket " +
			"requester-pays: every request must then be signed by a caller that accepts the charge. The default, " +
			"`BucketOwner`, bills the bucket owner. Destroying this resource restores `BucketOwner`; the bucket itself " +
			"is never deleted.",
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
			"payer": schema.StringAttribute{
				Required: true,
				Description: "Who pays for requests: BucketOwner (the default) or Requester. `Requester` changes who " +
					"gets billed, so set it deliberately.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.Payer("").Values())...),
				},
			},
		},
	}
}

func (r *bucketRequestPaymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *bucketRequestPaymentResource) put(ctx context.Context, bucket, payer string) error {
	_, err := r.client.S3.PutBucketRequestPayment(ctx, &s3.PutBucketRequestPaymentInput{
		Bucket: aws.String(bucket),
		RequestPaymentConfiguration: &s3types.RequestPaymentConfiguration{
			Payer: s3types.Payer(payer),
		},
	})
	return err
}

func (r *bucketRequestPaymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketRequestPaymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := plan.Bucket.ValueString()
	if err := r.put(ctx, bucket, plan.Payer.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Error creating bucket request payment configuration",
			"Could not set the payer: "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "created a bucket request payment configuration resource")

	plan.Id = types.StringValue(bucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketRequestPaymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketRequestPaymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.client.S3.GetBucketRequestPayment(ctx, &s3.GetBucketRequestPaymentInput{
		Bucket: aws.String(state.Bucket.ValueString()),
	})
	if err != nil {
		if isBucketSubresourceAbsent(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket request payment configuration",
			"Could not read the payer: "+err.Error(),
		)
		return
	}

	// The route always reports a payer; BucketOwner is the server default. The
	// resource keeps it, so an imported bucket is described as it behaves.
	state.Payer = types.StringValue(string(out.Payer))
	state.Id = types.StringValue(state.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketRequestPaymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketRequestPaymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := plan.Bucket.ValueString()
	if err := r.put(ctx, bucket, plan.Payer.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket request payment configuration",
			"Could not set the payer: "+err.Error(),
		)
		return
	}

	plan.Id = types.StringValue(bucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketRequestPaymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketRequestPaymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Restoring the default is the only "off" the route offers; setting it on a
	// bucket that is already BucketOwner is a no-op, which keeps destroy
	// idempotent.
	if err := r.put(ctx, data.Bucket.ValueString(), string(s3types.PayerBucketOwner)); err != nil {
		if isBucketSubresourceAbsent(err) {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket request payment configuration",
			"Could not restore the BucketOwner payer: "+err.Error(),
		)
	}
}

func (r *bucketRequestPaymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func init() {
	registerResource(NewBucketRequestPaymentResource)
}
