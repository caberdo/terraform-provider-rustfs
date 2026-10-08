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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &bucketPublicAccessBlockRessource{}
	_ resource.ResourceWithImportState = &bucketPublicAccessBlockRessource{}
)

// NewBucketPublicAccessBlockRessource is a helper function to simplify the provider implementation.
func NewBucketPublicAccessBlockRessource() resource.Resource {
	return &bucketPublicAccessBlockRessource{}
}

// bucketPublicAccessBlockRessource is the resource implementation.
type bucketPublicAccessBlockRessource struct {
	client *AllClient
}

type bucketPublicAccessBlockModel struct {
	Bucket                types.String `tfsdk:"bucket"`
	Id                    types.String `tfsdk:"id"`
	BlockPublicAcls       types.Bool   `tfsdk:"block_public_acls"`
	IgnorePublicAcls      types.Bool   `tfsdk:"ignore_public_acls"`
	BlockPublicPolicy     types.Bool   `tfsdk:"block_public_policy"`
	RestrictPublicBuckets types.Bool   `tfsdk:"restrict_public_buckets"`
}

// Metadata returns the resource type name.
func (r *bucketPublicAccessBlockRessource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_public_access_block"
}

// Schema defines the schema for the resource.
func (r *bucketPublicAccessBlockRessource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage the block public access configuration of an S3 bucket in rustfs",
		MarkdownDescription: "Manage the block public access configuration of an S3 bucket in rustfs",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Description: "Name of the bucket",
			},
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Description: "The bucket name",
			},
			"block_public_acls": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether Amazon S3 should block public ACLs for this bucket",
			},
			"ignore_public_acls": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether Amazon S3 should ignore public ACLs for this bucket",
			},
			"block_public_policy": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether Amazon S3 should block public bucket policies for this bucket",
			},
			"restrict_public_buckets": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether Amazon S3 should restrict public bucket policies for this bucket",
			},
		},
	}
}

func (r *bucketPublicAccessBlockRessource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func buildPublicAccessBlock(plan bucketPublicAccessBlockModel) *s3types.PublicAccessBlockConfiguration {
	config := &s3types.PublicAccessBlockConfiguration{}
	if !plan.BlockPublicAcls.IsNull() && !plan.BlockPublicAcls.IsUnknown() {
		config.BlockPublicAcls = aws.Bool(plan.BlockPublicAcls.ValueBool())
	}
	if !plan.IgnorePublicAcls.IsNull() && !plan.IgnorePublicAcls.IsUnknown() {
		config.IgnorePublicAcls = aws.Bool(plan.IgnorePublicAcls.ValueBool())
	}
	if !plan.BlockPublicPolicy.IsNull() && !plan.BlockPublicPolicy.IsUnknown() {
		config.BlockPublicPolicy = aws.Bool(plan.BlockPublicPolicy.ValueBool())
	}
	if !plan.RestrictPublicBuckets.IsNull() && !plan.RestrictPublicBuckets.IsUnknown() {
		config.RestrictPublicBuckets = aws.Bool(plan.RestrictPublicBuckets.ValueBool())
	}
	return config
}

// readPublicAccessBlock refreshes state from the server. A nil payload means the
// bucket has no explicit configuration, in which case every flag takes its
// effective server default of false.
func (r *bucketPublicAccessBlockRessource) readPublicAccessBlock(ctx context.Context, state *bucketPublicAccessBlockModel) error {
	out, err := r.client.S3.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{
		Bucket: aws.String(state.Bucket.ValueString()),
	})
	if err != nil {
		return err
	}

	config := out.PublicAccessBlockConfiguration
	if config == nil {
		state.BlockPublicAcls = types.BoolValue(false)
		state.IgnorePublicAcls = types.BoolValue(false)
		state.BlockPublicPolicy = types.BoolValue(false)
		state.RestrictPublicBuckets = types.BoolValue(false)
	} else {
		state.BlockPublicAcls = types.BoolValue(aws.ToBool(config.BlockPublicAcls))
		state.IgnorePublicAcls = types.BoolValue(aws.ToBool(config.IgnorePublicAcls))
		state.BlockPublicPolicy = types.BoolValue(aws.ToBool(config.BlockPublicPolicy))
		state.RestrictPublicBuckets = types.BoolValue(aws.ToBool(config.RestrictPublicBuckets))
	}
	return nil
}

// Create creates the resource and sets the initial Terraform state.
func (r *bucketPublicAccessBlockRessource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketPublicAccessBlockModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket:                         aws.String(plan.Bucket.ValueString()),
		PublicAccessBlockConfiguration: buildPublicAccessBlock(plan),
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating bucket public access block",
			"Could not set public access block: "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "created a bucket public access block resource")

	plan.Id = types.StringValue(plan.Bucket.ValueString())
	if err := r.readPublicAccessBlock(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading bucket public access block after create",
			"Could not read public access block: "+err.Error(),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the Terraform state with the latest data.
func (r *bucketPublicAccessBlockRessource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketPublicAccessBlockModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readPublicAccessBlock(ctx, &state); err != nil {
		if isBucketSubresourceAbsent(err, "NoSuchPublicAccessBlockConfiguration") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket public access block",
			"Could not read public access block: "+err.Error(),
		)
		return
	}

	state.Id = types.StringValue(state.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *bucketPublicAccessBlockRessource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketPublicAccessBlockModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket:                         aws.String(plan.Bucket.ValueString()),
		PublicAccessBlockConfiguration: buildPublicAccessBlock(plan),
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket public access block",
			"Could not set public access block: "+err.Error(),
		)
		return
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString())
	if err := r.readPublicAccessBlock(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading bucket public access block after update",
			"Could not read public access block: "+err.Error(),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete deletes the resource and removes the Terraform state on success.
func (r *bucketPublicAccessBlockRessource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketPublicAccessBlockModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.DeletePublicAccessBlock(ctx, &s3.DeletePublicAccessBlockInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		if isBucketSubresourceAbsent(err, "NoSuchPublicAccessBlockConfiguration") {
			// Already deleted
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket public access block",
			"Could not delete public access block: "+err.Error(),
		)
		return
	}
}

func (r *bucketPublicAccessBlockRessource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}
