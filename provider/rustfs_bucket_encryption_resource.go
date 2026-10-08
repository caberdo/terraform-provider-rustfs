package provider

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
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
	_ resource.Resource                = &BucketEncryptionResource{}
	_ resource.ResourceWithImportState = &BucketEncryptionResource{}
)

type BucketEncryptionResource struct {
	client *AllClient
}

type BucketEncryptionResourceModel struct {
	Bucket              types.String                `tfsdk:"bucket"`
	Rule                []BucketEncryptionRuleModel `tfsdk:"rule"`
	ChecksumAlgorithm   types.String                `tfsdk:"checksum_algorithm"`
	ContentMD5          types.String                `tfsdk:"content_md5"`
	ExpectedBucketOwner types.String                `tfsdk:"expected_bucket_owner"`
}

type BucketEncryptionRuleModel struct {
	ApplyServerSideEncryptionByDefault *BucketEncryptionByDefaultModel `tfsdk:"apply_server_side_encryption_by_default"`
	BucketKeyEnabled                   types.Bool                      `tfsdk:"bucket_key_enabled"`
}

type BucketEncryptionByDefaultModel struct {
	SSEAlgorithm   types.String `tfsdk:"sse_algorithm"`
	KMSMasterKeyID types.String `tfsdk:"kms_master_key_id"`
}

func NewBucketEncryptionResource() resource.Resource {
	return &BucketEncryptionResource{}
}

func (r *BucketEncryptionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_encryption"
}

// unsupported: s3types.ServerSideEncryptionRule.BlockedEncryptionTypes is
// omitted because it only blocks SSE-C uploads, an AWS general-purpose-bucket
// feature that RustFS/MinIO does not implement. s3.PutBucketEncryptionInput has
// no other AWS/directory-bucket-only members.
func (r *BucketEncryptionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage RustFS bucket encryption",
		MarkdownDescription: "Manage RustFS bucket server-side encryption configuration",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"rule": schema.ListNestedAttribute{
				Required:    true,
				Description: "Ordered list of server-side encryption rules. At least one rule is required.",
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"apply_server_side_encryption_by_default": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Default server-side encryption applied to new objects in the bucket.",
							Attributes: map[string]schema.Attribute{
								"sse_algorithm": schema.StringAttribute{
									Required:    true,
									Description: "Server-side encryption algorithm (for example AES256 or aws:kms).",
									Validators: []validator.String{
										stringvalidator.OneOf(enumStrings(s3types.ServerSideEncryption("").Values())...),
									},
								},
								"kms_master_key_id": schema.StringAttribute{
									Optional:    true,
									Computed:    true,
									Description: "KMS customer managed key ID, key alias or ARN. Only valid when sse_algorithm is aws:kms.",
								},
							},
						},
						"bucket_key_enabled": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Whether Amazon S3 should use an S3 Bucket Key for SSE-KMS encryption of new objects.",
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
				Description: "Base64 encoded 128-bit MD5 digest of the server-side encryption configuration.",
			},
			"expected_bucket_owner": schema.StringAttribute{
				Optional:    true,
				Description: "Account ID of the expected bucket owner. The request fails if it does not match the actual owner.",
			},
		},
	}
}

func (r *BucketEncryptionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *BucketEncryptionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan BucketEncryptionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.PutBucketEncryption(ctx, buildPutBucketEncryptionInput(plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error setting bucket encryption",
			"Could not set bucket encryption: "+err.Error(),
		)
		return
	}

	// Persist the plan; only Read mirrors the server. Refreshing here would let
	// server-side normalization diverge from the plan and produce an
	// "inconsistent result after apply" error.
	resolveEncryptionUnknowns(&plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// refreshEncryption replaces the model's rules with every rule the server
// returns, so Read state matches import. Write-only request fields are
// preserved. Create and Update deliberately do not call it: they persist the
// plan and let Read mirror the server, avoiding plan/state divergence.
func (r *BucketEncryptionResource) refreshEncryption(ctx context.Context, model *BucketEncryptionResourceModel) error {
	input := &s3.GetBucketEncryptionInput{
		Bucket: aws.String(model.Bucket.ValueString()),
	}
	if v := model.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}

	config, err := r.client.S3.GetBucketEncryption(ctx, input)
	if err != nil {
		return err
	}

	var rules []BucketEncryptionRuleModel
	if config.ServerSideEncryptionConfiguration != nil {
		for _, sr := range config.ServerSideEncryptionConfiguration.Rules {
			rule := BucketEncryptionRuleModel{}
			if sr.ApplyServerSideEncryptionByDefault != nil {
				kms := types.StringNull()
				if sr.ApplyServerSideEncryptionByDefault.KMSMasterKeyID != nil {
					kms = types.StringValue(*sr.ApplyServerSideEncryptionByDefault.KMSMasterKeyID)
				}
				rule.ApplyServerSideEncryptionByDefault = &BucketEncryptionByDefaultModel{
					SSEAlgorithm:   types.StringValue(string(sr.ApplyServerSideEncryptionByDefault.SSEAlgorithm)),
					KMSMasterKeyID: kms,
				}
			}
			if sr.BucketKeyEnabled != nil {
				rule.BucketKeyEnabled = types.BoolValue(*sr.BucketKeyEnabled)
			} else {
				rule.BucketKeyEnabled = types.BoolNull()
			}
			rules = append(rules, rule)
		}
	}
	model.Rule = rules
	return nil
}

func (r *BucketEncryptionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state BucketEncryptionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.refreshEncryption(ctx, &state); err != nil {
		if isBucketSubresourceAbsent(err, "ServerSideEncryptionConfigurationNotFoundError") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket encryption",
			"Could not read bucket encryption: "+err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// resolveEncryptionUnknowns replaces any unresolved Optional+Computed value
// with null so Create/Update never write an unknown into state. The applied
// state then equals the plan; only Read mirrors the server.
func resolveEncryptionUnknowns(model *BucketEncryptionResourceModel) {
	for i := range model.Rule {
		if model.Rule[i].BucketKeyEnabled.IsUnknown() {
			model.Rule[i].BucketKeyEnabled = types.BoolNull()
		}
		if apply := model.Rule[i].ApplyServerSideEncryptionByDefault; apply != nil && apply.KMSMasterKeyID.IsUnknown() {
			apply.KMSMasterKeyID = types.StringNull()
		}
	}
}

func (r *BucketEncryptionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan BucketEncryptionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.PutBucketEncryption(ctx, buildPutBucketEncryptionInput(plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket encryption",
			"Could not update bucket encryption: "+err.Error(),
		)
		return
	}

	resolveEncryptionUnknowns(&plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BucketEncryptionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketEncryptionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.DeleteBucketEncryption(ctx, &s3.DeleteBucketEncryptionInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error removing bucket encryption",
			"Could not remove bucket encryption: "+err.Error(),
		)
		return
	}
}

func (r *BucketEncryptionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func buildEncryptionConfig(plan BucketEncryptionResourceModel) *s3types.ServerSideEncryptionConfiguration {
	rules := make([]s3types.ServerSideEncryptionRule, 0, len(plan.Rule))
	for _, r := range plan.Rule {
		rule := s3types.ServerSideEncryptionRule{}
		if r.ApplyServerSideEncryptionByDefault != nil {
			apply := &s3types.ServerSideEncryptionByDefault{
				SSEAlgorithm: s3types.ServerSideEncryption(r.ApplyServerSideEncryptionByDefault.SSEAlgorithm.ValueString()),
			}
			if kms := r.ApplyServerSideEncryptionByDefault.KMSMasterKeyID.ValueString(); kms != "" {
				apply.KMSMasterKeyID = aws.String(kms)
			}
			rule.ApplyServerSideEncryptionByDefault = apply
		}
		if !r.BucketKeyEnabled.IsNull() && !r.BucketKeyEnabled.IsUnknown() {
			rule.BucketKeyEnabled = aws.Bool(r.BucketKeyEnabled.ValueBool())
		}
		rules = append(rules, rule)
	}
	return &s3types.ServerSideEncryptionConfiguration{Rules: rules}
}

func buildPutBucketEncryptionInput(plan BucketEncryptionResourceModel) *s3.PutBucketEncryptionInput {
	input := &s3.PutBucketEncryptionInput{
		Bucket:                            aws.String(plan.Bucket.ValueString()),
		ServerSideEncryptionConfiguration: buildEncryptionConfig(plan),
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
	return input
}
