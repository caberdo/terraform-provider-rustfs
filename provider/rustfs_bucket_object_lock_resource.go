package provider

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &BucketObjectLockResource{}
	_ resource.ResourceWithImportState = &BucketObjectLockResource{}
)

type BucketObjectLockResource struct {
	client *AllClient
}

type BucketObjectLockResourceModel struct {
	Bucket              types.String               `tfsdk:"bucket"`
	ObjectLockEnabled   types.String               `tfsdk:"object_lock_enabled"`
	Rule                *BucketObjectLockRuleModel `tfsdk:"rule"`
	ChecksumAlgorithm   types.String               `tfsdk:"checksum_algorithm"`
	ContentMD5          types.String               `tfsdk:"content_md5"`
	ExpectedBucketOwner types.String               `tfsdk:"expected_bucket_owner"`
}

type BucketObjectLockRuleModel struct {
	DefaultRetention *BucketObjectLockDefaultRetentionModel `tfsdk:"default_retention"`
}

type BucketObjectLockDefaultRetentionModel struct {
	Mode  types.String `tfsdk:"mode"`
	Days  types.Int64  `tfsdk:"days"`
	Years types.Int64  `tfsdk:"years"`
}

func NewBucketObjectLockResource() resource.Resource {
	return &BucketObjectLockResource{}
}

func (r *BucketObjectLockResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_object_lock"
}

// unsupported: s3.PutObjectLockConfigurationInput.Token (an AWS-only token used
// to enable Object Lock on an existing bucket) and .RequestPayer (AWS
// requester-pays) are omitted, as is s3types.DefaultRetention.DefaultEventHold,
// which carries AWS-specific Object Lock event-hold durations that RustFS/MinIO
// does not support.
func (r *BucketObjectLockResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage RustFS bucket object lock",
		MarkdownDescription: "Manage RustFS bucket object lock configuration",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket. Bucket must have been created with object lock enabled.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"object_lock_enabled": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether this bucket has an Object Lock configuration enabled. RustFS only accepts Enabled.",
				Validators: []validator.String{
					stringvalidator.OneOf(string(s3types.ObjectLockEnabledEnabled)),
				},
			},
			"rule": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Object Lock rule for the bucket.",
				Attributes: map[string]schema.Attribute{
					"default_retention": schema.SingleNestedAttribute{
						Required:    true,
						Description: "Default Object Lock retention settings for new objects.",
						Attributes: map[string]schema.Attribute{
							"mode": schema.StringAttribute{
								Required:    true,
								Description: "Default Object Lock retention mode: GOVERNANCE or COMPLIANCE.",
								Validators: []validator.String{
									stringvalidator.OneOf(enumStrings(s3types.ObjectLockRetentionMode("").Values())...),
								},
							},
							"days": schema.Int64Attribute{
								Optional:    true,
								Description: "Default retention period in days. Mutually exclusive with years.",
								Validators: []validator.Int64{
									int64validator.ConflictsWith(path.MatchRelative().AtParent().AtName("years")),
									int64validator.AtLeastOneOf(
										path.MatchRelative().AtParent().AtName("days"),
										path.MatchRelative().AtParent().AtName("years"),
									),
								},
							},
							"years": schema.Int64Attribute{
								Optional:    true,
								Description: "Default retention period in years. Mutually exclusive with days.",
								Validators: []validator.Int64{
									int64validator.ConflictsWith(path.MatchRelative().AtParent().AtName("days")),
									int64validator.AtLeastOneOf(
										path.MatchRelative().AtParent().AtName("days"),
										path.MatchRelative().AtParent().AtName("years"),
									),
								},
							},
						},
					},
				},
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
				Description: "MD5 hash for the request body.",
			},
			"expected_bucket_owner": schema.StringAttribute{
				Optional:    true,
				Description: "Account ID of the expected bucket owner. The request fails if it does not match the actual owner.",
			},
		},
	}
}

func (r *BucketObjectLockResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *BucketObjectLockResource) setConfig(ctx context.Context, plan BucketObjectLockResourceModel) error {
	input, err := buildPutObjectLockConfigurationInput(plan)
	if err != nil {
		return err
	}
	_, err = r.client.S3.PutObjectLockConfiguration(ctx, input)
	return err
}

// refreshObjectLock mirrors the server-side Object Lock configuration into the
// model so Create/Update state matches import. Write-only request fields are
// preserved.
func (r *BucketObjectLockResource) refreshObjectLock(ctx context.Context, model *BucketObjectLockResourceModel) error {
	input := &s3.GetObjectLockConfigurationInput{
		Bucket: aws.String(model.Bucket.ValueString()),
	}
	if v := model.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}

	out, err := r.client.S3.GetObjectLockConfiguration(ctx, input)
	if err != nil {
		return err
	}

	config := out.ObjectLockConfiguration
	if config == nil {
		model.ObjectLockEnabled = types.StringNull()
		model.Rule = nil
		return nil
	}

	if config.ObjectLockEnabled == "" {
		model.ObjectLockEnabled = types.StringNull()
	} else {
		model.ObjectLockEnabled = types.StringValue(string(config.ObjectLockEnabled))
	}

	if config.Rule == nil || config.Rule.DefaultRetention == nil {
		model.Rule = nil
		return nil
	}

	retention := config.Rule.DefaultRetention
	defaultRetention := &BucketObjectLockDefaultRetentionModel{
		Mode:  types.StringNull(),
		Days:  types.Int64Null(),
		Years: types.Int64Null(),
	}
	if retention.Mode != "" {
		defaultRetention.Mode = types.StringValue(string(retention.Mode))
	}
	if retention.Days != nil {
		defaultRetention.Days = types.Int64Value(int64(*retention.Days))
	}
	if retention.Years != nil {
		defaultRetention.Years = types.Int64Value(int64(*retention.Years))
	}
	model.Rule = &BucketObjectLockRuleModel{DefaultRetention: defaultRetention}
	return nil
}

func (r *BucketObjectLockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan BucketObjectLockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.setConfig(ctx, plan); err != nil {
		resp.Diagnostics.AddError(
			"Error setting object lock",
			"Could not set object lock: "+err.Error(),
		)
		return
	}

	if err := r.refreshObjectLock(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading object lock",
			"Could not read object lock after create: "+err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BucketObjectLockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state BucketObjectLockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.refreshObjectLock(ctx, &state); err != nil {
		resp.Diagnostics.AddError(
			"Error reading object lock",
			"Could not read object lock: "+err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketObjectLockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan BucketObjectLockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.setConfig(ctx, plan); err != nil {
		resp.Diagnostics.AddError(
			"Error updating object lock",
			"Could not update object lock: "+err.Error(),
		)
		return
	}

	if err := r.refreshObjectLock(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading object lock",
			"Could not read object lock after update: "+err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BucketObjectLockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Object lock cannot be removed from a bucket; only the bucket itself can be deleted.
}

func (r *BucketObjectLockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func buildObjectLockConfiguration(plan BucketObjectLockResourceModel) (*s3types.ObjectLockConfiguration, error) {
	config := &s3types.ObjectLockConfiguration{
		ObjectLockEnabled: s3types.ObjectLockEnabledEnabled,
	}
	if v := plan.ObjectLockEnabled.ValueString(); v != "" {
		config.ObjectLockEnabled = s3types.ObjectLockEnabled(v)
	}

	if plan.Rule == nil || plan.Rule.DefaultRetention == nil {
		return config, nil
	}

	days := plan.Rule.DefaultRetention.Days.ValueInt64()
	years := plan.Rule.DefaultRetention.Years.ValueInt64()
	if days > 0 && years > 0 {
		return nil, fmt.Errorf("default retention must set either days or years, not both")
	}

	retention := &s3types.DefaultRetention{
		Mode: s3types.ObjectLockRetentionMode(plan.Rule.DefaultRetention.Mode.ValueString()),
	}
	if days > 0 {
		retention.Days = aws.Int32(int32(days)) // #nosec G115
	}
	if years > 0 {
		retention.Years = aws.Int32(int32(years)) // #nosec G115
	}
	config.Rule = &s3types.ObjectLockRule{DefaultRetention: retention}
	return config, nil
}

func buildPutObjectLockConfigurationInput(plan BucketObjectLockResourceModel) (*s3.PutObjectLockConfigurationInput, error) {
	config, err := buildObjectLockConfiguration(plan)
	if err != nil {
		return nil, err
	}
	input := &s3.PutObjectLockConfigurationInput{
		Bucket:                  aws.String(plan.Bucket.ValueString()),
		ObjectLockConfiguration: config,
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
	return input, nil
}
