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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &BucketReplicationResource{}
	_ resource.ResourceWithImportState = &BucketReplicationResource{}
)

type BucketReplicationResource struct {
	client *AllClient
}

type bucketReplicationResourceModel struct {
	Bucket                  types.String `tfsdk:"bucket"`
	Role                    types.String `tfsdk:"role"`
	DestinationBucket       types.String `tfsdk:"destination_bucket"`
	Priority                types.Int64  `tfsdk:"priority"`
	Status                  types.String `tfsdk:"status"`
	DeleteMarkerReplication types.String `tfsdk:"delete_marker_replication"`
	DeleteReplication       types.String `tfsdk:"delete_replication"`
}

func NewBucketReplicationResource() resource.Resource {
	return &BucketReplicationResource{}
}

func (r *BucketReplicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_replication"
}

func (r *BucketReplicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage RustFS bucket replication",
		MarkdownDescription: "Manage RustFS bucket replication configuration",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:      true,
				Description:   "Name of the source bucket.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				Required:    true,
				Description: "Replication role ARN.",
			},
			"destination_bucket": schema.StringAttribute{
				Required:    true,
				Description: "Destination bucket ARN.",
			},
			"priority": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(1),
				Description: "Rule priority.",
			},
			"status": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("Enabled"),
				Description: "Rule status: Enabled or Disabled.",
			},
			"delete_marker_replication": schema.StringAttribute{
				Optional:    true,
				Description: "Delete marker replication: Enabled or Disabled.",
			},
			"delete_replication": schema.StringAttribute{
				Optional:    true,
				Description: "Delete replication: Enabled or Disabled.",
			},
		},
	}
}

func (r *BucketReplicationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *AllClient, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *BucketReplicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketReplicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	warnUnsupportedDeleteReplication(ctx, plan)

	cfg := buildReplicationConfig(plan)
	if _, err := r.client.S3.PutBucketReplication(ctx, &s3.PutBucketReplicationInput{
		Bucket:                   aws.String(plan.Bucket.ValueString()),
		ReplicationConfiguration: cfg,
	}); err != nil {
		resp.Diagnostics.AddError("Error setting bucket replication", "Could not set replication: "+err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func warnUnsupportedDeleteReplication(ctx context.Context, plan bucketReplicationResourceModel) {
	if plan.DeleteReplication.ValueString() != "" {
		tflog.Warn(ctx, "delete_replication is a MinIO extension that the AWS SDK S3 client cannot express; the value is kept in state but not applied")
	}
}

func (r *BucketReplicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketReplicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg, err := r.client.S3.GetBucketReplication(ctx, &s3.GetBucketReplicationInput{
		Bucket: aws.String(state.Bucket.ValueString()),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error reading bucket replication", "Could not read: "+err.Error())
		return
	}

	if config := cfg.ReplicationConfiguration; config != nil {
		state.Role = types.StringValue(aws.ToString(config.Role))
		if len(config.Rules) > 0 {
			rule := config.Rules[0]
			state.Status = types.StringValue(string(rule.Status))
			if rule.Priority != nil {
				state.Priority = types.Int64Value(int64(*rule.Priority))
			}
			if rule.DeleteMarkerReplication != nil {
				state.DeleteMarkerReplication = types.StringValue(string(rule.DeleteMarkerReplication.Status))
			} else {
				state.DeleteMarkerReplication = types.StringValue("")
			}
			if rule.Destination != nil && aws.ToString(rule.Destination.Bucket) != "" {
				state.DestinationBucket = types.StringValue(aws.ToString(rule.Destination.Bucket))
			}
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketReplicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketReplicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	warnUnsupportedDeleteReplication(ctx, plan)

	cfg := buildReplicationConfig(plan)
	if _, err := r.client.S3.PutBucketReplication(ctx, &s3.PutBucketReplicationInput{
		Bucket:                   aws.String(plan.Bucket.ValueString()),
		ReplicationConfiguration: cfg,
	}); err != nil {
		resp.Diagnostics.AddError("Error updating bucket replication", "Could not update: "+err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BucketReplicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketReplicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.DeleteBucketReplication(ctx, &s3.DeleteBucketReplicationInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	}); err != nil {
		resp.Diagnostics.AddError("Error removing bucket replication", "Could not remove: "+err.Error())
		return
	}
}

func (r *BucketReplicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func buildReplicationConfig(plan bucketReplicationResourceModel) *s3types.ReplicationConfiguration {
	rule := s3types.ReplicationRule{
		ID:       aws.String("rule-1"),
		Status:   s3types.ReplicationRuleStatus(plan.Status.ValueString()),
		Priority: aws.Int32(int32(plan.Priority.ValueInt64())), // #nosec G115
		Destination: &s3types.Destination{
			Bucket: aws.String(plan.DestinationBucket.ValueString()),
		},
	}

	if v := plan.DeleteMarkerReplication.ValueString(); v != "" {
		rule.DeleteMarkerReplication = &s3types.DeleteMarkerReplication{
			Status: s3types.DeleteMarkerReplicationStatus(v),
		}
	}

	return &s3types.ReplicationConfiguration{
		Role:  aws.String(plan.Role.ValueString()),
		Rules: []s3types.ReplicationRule{rule},
	}
}
