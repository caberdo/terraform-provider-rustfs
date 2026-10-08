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
	_ resource.Resource                = &BucketVersioningResource{}
	_ resource.ResourceWithImportState = &BucketVersioningResource{}
)

type BucketVersioningResource struct {
	client *AllClient
}

type BucketVersioningResourceModel struct {
	Bucket              types.String `tfsdk:"bucket"`
	Status              types.String `tfsdk:"status"`
	MfaDelete           types.String `tfsdk:"mfa_delete"`
	Mfa                 types.String `tfsdk:"mfa"`
	ChecksumAlgorithm   types.String `tfsdk:"checksum_algorithm"`
	ContentMD5          types.String `tfsdk:"content_md5"`
	ExpectedBucketOwner types.String `tfsdk:"expected_bucket_owner"`
}

func NewBucketVersioningResource() resource.Resource {
	return &BucketVersioningResource{}
}

func (r *BucketVersioningResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_versioning"
}

// unsupported: none. Every member of s3.PutBucketVersioningInput and
// s3types.VersioningConfiguration is mapped; neither struct carries
// AWS-only or directory-bucket-only fields for this operation.
func (r *BucketVersioningResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage RustFS bucket versioning",
		MarkdownDescription: "Manage RustFS bucket versioning configuration",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"status": schema.StringAttribute{
				Required:    true,
				Description: "Versioning status: Enabled or Suspended.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.BucketVersioningStatus("").Values())...),
				},
			},
			"mfa_delete": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Specifies whether MFA delete is enabled in the bucket versioning configuration. Enabled or Disabled.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.MFADelete("").Values())...),
				},
			},
			"mfa": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Concatenation of the authentication device serial number, a space, and the value displayed on the MFA device. Required to change MFA delete state.",
			},
			"checksum_algorithm": schema.StringAttribute{
				Optional:    true,
				Description: "Checksum algorithm used by the SDK when sending the request.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.ChecksumAlgorithm("").Values())...),
				},
			},
			"content_md5": schema.StringAttribute{
				Optional:    true,
				Description: "Base64 encoded 128-bit MD5 digest of the request body, used as a message integrity check.",
			},
			"expected_bucket_owner": schema.StringAttribute{
				Optional:    true,
				Description: "Account ID of the expected bucket owner. The request fails if it does not match the actual owner.",
			},
		},
	}
}

func (r *BucketVersioningResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T.", req.ProviderData),
		)
		return
	}
	r.client = client
}

func (r *BucketVersioningResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan BucketVersioningResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.PutBucketVersioning(ctx, buildPutBucketVersioningInput(plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error setting bucket versioning",
			"Could not set bucket versioning: "+err.Error(),
		)
		return
	}

	if err := r.refreshVersioning(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading bucket versioning",
			"Could not read bucket versioning after create: "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "created bucket versioning")
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// refreshVersioning mirrors the server-side versioning state into the model.
// Only GetBucketVersioning-observable fields are refreshed; the write-only
// request fields are left untouched so Create/Update state matches import.
func (r *BucketVersioningResource) refreshVersioning(ctx context.Context, model *BucketVersioningResourceModel) error {
	input := &s3.GetBucketVersioningInput{
		Bucket: aws.String(model.Bucket.ValueString()),
	}
	if v := model.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}

	config, err := r.client.S3.GetBucketVersioning(ctx, input)
	if err != nil {
		return err
	}

	applyVersioningOutput(model, config)
	return nil
}

// applyVersioningOutput maps a GetBucketVersioning response onto the model.
// An omitted MFADelete means the bucket is not MFA-delete protected, which is
// the semantic default "Disabled"; storing null instead would make an explicit
// mfa_delete = "Disabled" plan differ from state forever.
func applyVersioningOutput(model *BucketVersioningResourceModel, config *s3.GetBucketVersioningOutput) {
	if config == nil {
		return
	}
	if config.Status != "" {
		model.Status = types.StringValue(string(config.Status))
	}
	if config.MFADelete == "" {
		model.MfaDelete = types.StringValue(string(s3types.MFADeleteDisabled))
	} else {
		model.MfaDelete = types.StringValue(string(config.MFADelete))
	}
}

func (r *BucketVersioningResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state BucketVersioningResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.refreshVersioning(ctx, &state); err != nil {
		if isBucketSubresourceAbsent(err, "NoSuchBucket") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket versioning",
			"Could not read bucket versioning: "+err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketVersioningResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan BucketVersioningResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.PutBucketVersioning(ctx, buildPutBucketVersioningInput(plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket versioning",
			"Could not update bucket versioning: "+err.Error(),
		)
		return
	}

	if err := r.refreshVersioning(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading bucket versioning",
			"Could not read bucket versioning after update: "+err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BucketVersioningResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketVersioningResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(data.Bucket.ValueString()),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusSuspended,
		},
	})
	if err != nil {
		if isBucketSubresourceAbsent(err, "NoSuchBucket") {
			return
		}
		resp.Diagnostics.AddError(
			"Error suspending bucket versioning",
			"Could not suspend bucket versioning: "+err.Error(),
		)
		return
	}
}

func (r *BucketVersioningResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func buildVersioningConfiguration(plan BucketVersioningResourceModel) *s3types.VersioningConfiguration {
	config := &s3types.VersioningConfiguration{
		Status: s3types.BucketVersioningStatus(plan.Status.ValueString()),
	}
	if v := plan.MfaDelete.ValueString(); v != "" {
		config.MFADelete = s3types.MFADelete(v)
	}
	return config
}

func buildPutBucketVersioningInput(plan BucketVersioningResourceModel) *s3.PutBucketVersioningInput {
	input := &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(plan.Bucket.ValueString()),
		VersioningConfiguration: buildVersioningConfiguration(plan),
	}
	if v := plan.ChecksumAlgorithm.ValueString(); v != "" {
		input.ChecksumAlgorithm = s3types.ChecksumAlgorithm(v)
	}
	if v := plan.ContentMD5.ValueString(); v != "" {
		input.ContentMD5 = aws.String(v)
	}
	if v := plan.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}
	if v := plan.Mfa.ValueString(); v != "" {
		input.MFA = aws.String(v)
	}
	return input
}
