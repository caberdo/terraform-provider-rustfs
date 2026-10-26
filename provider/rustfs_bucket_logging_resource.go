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
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                = &bucketLoggingResource{}
	_ resource.ResourceWithImportState = &bucketLoggingResource{}
)

// NewBucketLoggingResource manages access logging for one bucket.
func NewBucketLoggingResource() resource.Resource {
	return &bucketLoggingResource{}
}

type bucketLoggingResource struct {
	client *AllClient
}

type bucketLoggingModel struct {
	Bucket       types.String       `tfsdk:"bucket"`
	Id           types.String       `tfsdk:"id"`
	TargetBucket types.String       `tfsdk:"target_bucket"`
	TargetPrefix types.String       `tfsdk:"target_prefix"`
	TargetGrant  []targetGrantModel `tfsdk:"target_grant"`
}

type targetGrantModel struct {
	Grantee    *targetGrantGranteeModel `tfsdk:"grantee"`
	Permission types.String             `tfsdk:"permission"`
}

type targetGrantGranteeModel struct {
	Type         types.String `tfsdk:"type"`
	Id           types.String `tfsdk:"id"`
	Uri          types.String `tfsdk:"uri"`
	EmailAddress types.String `tfsdk:"email_address"`
}

func (r *bucketLoggingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_logging"
}

func (r *bucketLoggingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage the server access logging configuration of an S3 bucket in rustfs",
		MarkdownDescription: "Send this bucket's access log to a target bucket. The resource never creates the target " +
			"bucket: it checks that the bucket exists and fails with a diagnostic naming it, so a typo cannot silently " +
			"log into nowhere. Destroying this resource disables logging; the bucket itself is never deleted.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket whose access is logged",
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
			"target_bucket": schema.StringAttribute{
				Required: true,
				Description: "Bucket that receives the log objects. It must already exist; this resource does not " +
					"create it.",
			},
			"target_prefix": schema.StringAttribute{
				Optional:    true,
				Description: "Key prefix the log objects are written under (for example `logs/`).",
			},
		},
		Blocks: map[string]schema.Block{
			"target_grant": schema.ListNestedBlock{
				Description: "Optional grants the log-delivery group needs on the target bucket.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"permission": schema.StringAttribute{
							Required:    true,
							Description: "Log-delivery permission: FULL_CONTROL, READ or WRITE.",
							Validators: []validator.String{
								stringvalidator.OneOf(enumStrings(s3types.BucketLogsPermission("").Values())...),
							},
						},
					},
					Blocks: map[string]schema.Block{
						"grantee": schema.SingleNestedBlock{
							Description: "Who the grant applies to.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Required:    true,
									Description: "Grantee type: CanonicalUser, AmazonCustomerByEmail or Group.",
									Validators: []validator.String{
										stringvalidator.OneOf(enumStrings(s3types.Type("").Values())...),
									},
								},
								"id": schema.StringAttribute{
									Optional:    true,
									Description: "Canonical user id, for a CanonicalUser grantee.",
								},
								"uri": schema.StringAttribute{
									Optional:    true,
									Description: "Group URI, for a Group grantee (for example `http://acs.amazonaws.com/groups/s3/LogDelivery`).",
								},
								"email_address": schema.StringAttribute{
									Optional:    true,
									Description: "Email address, for an AmazonCustomerByEmail grantee.",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *bucketLoggingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// buildLoggingStatus maps the model onto the S3 document.
func buildLoggingStatus(plan bucketLoggingModel) *s3types.BucketLoggingStatus {
	enabled := &s3types.LoggingEnabled{
		TargetBucket: aws.String(plan.TargetBucket.ValueString()),
	}
	if v := plan.TargetPrefix.ValueString(); v != "" {
		enabled.TargetPrefix = aws.String(v)
	}
	for _, grant := range plan.TargetGrant {
		flattened := s3types.TargetGrant{
			Permission: s3types.BucketLogsPermission(grant.Permission.ValueString()),
		}
		if grantee := grant.Grantee; grantee != nil {
			flattened.Grantee = &s3types.Grantee{
				Type: s3types.Type(grantee.Type.ValueString()),
			}
			if v := grantee.Id.ValueString(); v != "" {
				flattened.Grantee.ID = aws.String(v)
			}
			if v := grantee.Uri.ValueString(); v != "" {
				flattened.Grantee.URI = aws.String(v)
			}
			if v := grantee.EmailAddress.ValueString(); v != "" {
				flattened.Grantee.EmailAddress = aws.String(v)
			}
		}
		enabled.TargetGrants = append(enabled.TargetGrants, flattened)
	}
	return &s3types.BucketLoggingStatus{LoggingEnabled: enabled}
}

func flattenTargetGrants(grants []s3types.TargetGrant) []targetGrantModel {
	out := make([]targetGrantModel, 0, len(grants))
	for _, grant := range grants {
		flattened := targetGrantModel{
			Permission: types.StringValue(string(grant.Permission)),
		}
		if grant.Grantee != nil {
			flattened.Grantee = &targetGrantGranteeModel{
				Type:         types.StringValue(string(grant.Grantee.Type)),
				Id:           types.StringNull(),
				Uri:          types.StringNull(),
				EmailAddress: types.StringNull(),
			}
			if grant.Grantee.ID != nil {
				flattened.Grantee.Id = types.StringValue(*grant.Grantee.ID)
			}
			if grant.Grantee.URI != nil {
				flattened.Grantee.Uri = types.StringValue(*grant.Grantee.URI)
			}
			if grant.Grantee.EmailAddress != nil {
				flattened.Grantee.EmailAddress = types.StringValue(*grant.Grantee.EmailAddress)
			}
		}
		out = append(out, flattened)
	}
	return out
}

func (r *bucketLoggingResource) put(ctx context.Context, plan bucketLoggingModel, status *s3types.BucketLoggingStatus) error {
	_, err := r.client.S3.PutBucketLogging(ctx, &s3.PutBucketLoggingInput{
		Bucket:              aws.String(plan.Bucket.ValueString()),
		BucketLoggingStatus: status,
	})
	return err
}

// checkTargetBucket refuses to configure logging into a bucket that does not
// exist: RustFS accepts the write and would log into nothing.
func (r *bucketLoggingResource) checkTargetBucket(ctx context.Context, plan bucketLoggingModel, diags *diag.Diagnostics) bool {
	target := plan.TargetBucket.ValueString()
	exists, err := bucketExists(ctx, r.client.S3, target)
	if err != nil {
		diags.AddError(
			"Error checking logging target bucket",
			fmt.Sprintf("Could not check bucket %q: %s", target, err),
		)
		return false
	}
	if !exists {
		diags.AddError(
			"Logging target bucket does not exist",
			fmt.Sprintf("Bucket %q does not exist. Create it first, or make this resource depend on its "+
				"rustfs_bucket with depends_on, so a logging configuration never points at a bucket that is not there.",
				target),
		)
		return false
	}
	return true
}

func (r *bucketLoggingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketLoggingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.checkTargetBucket(ctx, plan, &resp.Diagnostics) {
		return
	}
	if err := r.put(ctx, plan, buildLoggingStatus(plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error creating bucket logging configuration",
			"Could not enable access logging: "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "created a bucket logging configuration resource")

	plan.Id = types.StringValue(plan.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketLoggingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketLoggingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.client.S3.GetBucketLogging(ctx, &s3.GetBucketLoggingInput{
		Bucket: aws.String(state.Bucket.ValueString()),
	})
	if err != nil {
		if isBucketSubresourceAbsent(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket logging configuration",
			"Could not read the access logging configuration: "+err.Error(),
		)
		return
	}

	// A bucket without logging reports no LoggingEnabled document at all.
	if out.LoggingEnabled == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.TargetBucket = types.StringValue(aws.ToString(out.LoggingEnabled.TargetBucket))
	state.TargetPrefix = types.StringNull()
	if out.LoggingEnabled.TargetPrefix != nil {
		state.TargetPrefix = types.StringValue(*out.LoggingEnabled.TargetPrefix)
	}
	state.TargetGrant = flattenTargetGrants(out.LoggingEnabled.TargetGrants)
	state.Id = types.StringValue(state.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketLoggingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketLoggingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.checkTargetBucket(ctx, plan, &resp.Diagnostics) {
		return
	}
	if err := r.put(ctx, plan, buildLoggingStatus(plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket logging configuration",
			"Could not update access logging: "+err.Error(),
		)
		return
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketLoggingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketLoggingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An empty status document is S3's way of disabling logging. A bucket that
	// is already gone is the goal state as well.
	if err := r.put(ctx, data, &s3types.BucketLoggingStatus{}); err != nil {
		if isBucketSubresourceAbsent(err) {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket logging configuration",
			"Could not disable access logging: "+err.Error(),
		)
	}
}

func (r *bucketLoggingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func init() {
	registerResource(NewBucketLoggingResource)
}
