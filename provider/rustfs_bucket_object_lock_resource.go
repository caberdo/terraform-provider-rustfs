package provider

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
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
	Bucket types.String `tfsdk:"bucket"`
	Mode   types.String `tfsdk:"mode"`
	Days   types.Int64  `tfsdk:"days"`
	Years  types.Int64  `tfsdk:"years"`
}

func NewBucketObjectLockResource() resource.Resource {
	return &BucketObjectLockResource{}
}

func (r *BucketObjectLockResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_object_lock"
}

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
			"mode": schema.StringAttribute{
				Required:    true,
				Description: "Object lock retention mode: COMPLIANCE or GOVERNANCE.",
			},
			"days": schema.Int64Attribute{
				Optional:    true,
				Description: "Retention period in days.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"years": schema.Int64Attribute{
				Optional:    true,
				Description: "Retention period in years.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
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
	daysVal := plan.Days.ValueInt64()
	yearsVal := plan.Years.ValueInt64()
	if daysVal < 0 {
		daysVal = 0
	}
	if yearsVal < 0 {
		yearsVal = 0
	}

	config := &s3types.ObjectLockConfiguration{
		ObjectLockEnabled: s3types.ObjectLockEnabledEnabled,
	}

	if daysVal > 0 {
		config.Rule = &s3types.ObjectLockRule{
			DefaultRetention: &s3types.DefaultRetention{
				Mode: s3types.ObjectLockRetentionMode(plan.Mode.ValueString()),
				Days: aws.Int32(int32(daysVal)), // #nosec G115
			},
		}
	} else if yearsVal > 0 {
		config.Rule = &s3types.ObjectLockRule{
			DefaultRetention: &s3types.DefaultRetention{
				Mode:  s3types.ObjectLockRetentionMode(plan.Mode.ValueString()),
				Years: aws.Int32(int32(yearsVal)), // #nosec G115
			},
		}
	}

	_, err := r.client.S3.PutObjectLockConfiguration(ctx, &s3.PutObjectLockConfigurationInput{
		Bucket:                  aws.String(plan.Bucket.ValueString()),
		ObjectLockConfiguration: config,
	})
	return err
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

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BucketObjectLockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state BucketObjectLockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.client.S3.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{
		Bucket: aws.String(state.Bucket.ValueString()),
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading object lock",
			"Could not read object lock: "+err.Error(),
		)
		return
	}

	if out.ObjectLockConfiguration != nil && out.ObjectLockConfiguration.Rule != nil {
		retention := out.ObjectLockConfiguration.Rule.DefaultRetention
		if retention != nil {
			state.Mode = types.StringValue(string(retention.Mode))
			if retention.Days != nil {
				state.Days = types.Int64Value(int64(*retention.Days))
			}
			if retention.Years != nil {
				state.Years = types.Int64Value(int64(*retention.Years))
			}
		}
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

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BucketObjectLockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Object lock cannot be removed from a bucket; only the bucket itself can be deleted.
}

func (r *BucketObjectLockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}
