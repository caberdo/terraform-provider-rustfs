package provider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                = &bucketCorsRessource{}
	_ resource.ResourceWithImportState = &bucketCorsRessource{}
)

func NewBucketCorsRessource() resource.Resource {
	return &bucketCorsRessource{}
}

type bucketCorsRessource struct {
	client *AllClient
}

type bucketCorsModel struct {
	Bucket types.String    `tfsdk:"bucket"`
	Id     types.String    `tfsdk:"id"`
	Rule   []corsRuleModel `tfsdk:"rule"`
}

type corsRuleModel struct {
	AllowedHeaders types.Set    `tfsdk:"allowed_headers"`
	AllowedMethods types.Set    `tfsdk:"allowed_methods"`
	AllowedOrigins types.Set    `tfsdk:"allowed_origins"`
	ExposeHeaders  types.Set    `tfsdk:"expose_headers"`
	MaxAgeSeconds  types.Int64  `tfsdk:"max_age_seconds"`
	Id             types.String `tfsdk:"id"`
}

func (r *bucketCorsRessource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_cors"
}

func (r *bucketCorsRessource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage the CORS configuration of an S3 bucket in rustfs",
		MarkdownDescription: "Manage the CORS configuration of an S3 bucket in rustfs",
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
		},
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				Description: "List of CORS rules",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"allowed_headers": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Specifies which headers are allowed",
						},
						"allowed_methods": schema.SetAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "Specifies which methods (GET, PUT, POST, DELETE, HEAD) are allowed",
						},
						"allowed_origins": schema.SetAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "Specifies which origins are allowed",
						},
						"expose_headers": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Specifies which headers are exposed to the application",
						},
						"max_age_seconds": schema.Int64Attribute{
							Optional: true,
							Validators: []validator.Int64{
								int64validator.Between(0, math.MaxInt32),
							},
							Description: "Time in seconds that the browser is allowed to cache the response",
						},
						"id": schema.StringAttribute{
							Optional:    true,
							Description: "Unique identifier for the rule",
						},
					},
				},
			},
		},
	}
}

func (r *bucketCorsRessource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func stringSetFromSlice(vals []string) types.Set {
	elements := make([]attr.Value, len(vals))
	for i, v := range vals {
		elements[i] = types.StringValue(v)
	}
	return types.SetValueMust(types.StringType, elements)
}

func stringSliceFromSet(ctx context.Context, s types.Set) []string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []string
	s.ElementsAs(ctx, &out, false)
	return out
}

// isBucketSubresourceAbsent reports whether err means a bucket sub-resource
// (CORS, tagging, policy or public access block) is not configured, either
// because the sub-resource itself is missing or because the bucket is gone.
// The SDK surfaces the sub-resource codes as smithy API errors; the string
// fallbacks keep working for endpoints that only carry the code in the payload.
func isBucketSubresourceAbsent(err error, codes ...string) bool {
	if err == nil {
		return false
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchBucket", "NotFound":
			return true
		}
		for _, code := range codes {
			if apiErr.ErrorCode() == code {
				return true
			}
		}
	}

	if strings.Contains(err.Error(), "NoSuchBucket") {
		return true
	}
	for _, code := range codes {
		if strings.Contains(err.Error(), code) {
			return true
		}
	}
	return false
}

func buildCorsConfig(ctx context.Context, plan bucketCorsModel) *s3types.CORSConfiguration {
	rules := make([]s3types.CORSRule, 0, len(plan.Rule))
	for _, rp := range plan.Rule {
		rule := s3types.CORSRule{
			AllowedHeaders: stringSliceFromSet(ctx, rp.AllowedHeaders),
			AllowedMethods: stringSliceFromSet(ctx, rp.AllowedMethods),
			AllowedOrigins: stringSliceFromSet(ctx, rp.AllowedOrigins),
			ExposeHeaders:  stringSliceFromSet(ctx, rp.ExposeHeaders),
		}
		if id := rp.Id.ValueString(); id != "" {
			rule.ID = aws.String(id)
		}
		if !rp.MaxAgeSeconds.IsNull() && !rp.MaxAgeSeconds.IsUnknown() {
			rule.MaxAgeSeconds = aws.Int32(int32(rp.MaxAgeSeconds.ValueInt64()))
		}
		rules = append(rules, rule)
	}
	return &s3types.CORSConfiguration{CORSRules: rules}
}

func flattenCorsRules(rules []s3types.CORSRule) []corsRuleModel {
	out := make([]corsRuleModel, 0, len(rules))
	for _, rc := range rules {
		id := types.StringNull()
		if rc.ID != nil && *rc.ID != "" {
			id = types.StringValue(*rc.ID)
		}
		maxAgeSeconds := types.Int64Null()
		if rc.MaxAgeSeconds != nil {
			maxAgeSeconds = types.Int64Value(int64(*rc.MaxAgeSeconds))
		}
		out = append(out, corsRuleModel{
			Id:             id,
			MaxAgeSeconds:  maxAgeSeconds,
			AllowedHeaders: stringSetFromSlice(rc.AllowedHeaders),
			AllowedMethods: stringSetFromSlice(rc.AllowedMethods),
			AllowedOrigins: stringSetFromSlice(rc.AllowedOrigins),
			ExposeHeaders:  stringSetFromSlice(rc.ExposeHeaders),
		})
	}
	return out
}

func (r *bucketCorsRessource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketCorsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket:            aws.String(plan.Bucket.ValueString()),
		CORSConfiguration: buildCorsConfig(ctx, plan),
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating bucket CORS configuration",
			"Could not create CORS configuration: "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "created a bucket CORS configuration resource")

	plan.Id = types.StringValue(plan.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketCorsRessource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketCorsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.client.S3.GetBucketCors(ctx, &s3.GetBucketCorsInput{
		Bucket: aws.String(state.Bucket.ValueString()),
	})
	if err != nil {
		if isBucketSubresourceAbsent(err, "NoSuchCORSConfiguration") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket CORS configuration",
			"Could not read CORS configuration: "+err.Error(),
		)
		return
	}

	state.Rule = flattenCorsRules(out.CORSRules)
	state.Id = types.StringValue(state.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketCorsRessource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketCorsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket:            aws.String(plan.Bucket.ValueString()),
		CORSConfiguration: buildCorsConfig(ctx, plan),
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket CORS configuration",
			"Could not update CORS configuration: "+err.Error(),
		)
		return
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketCorsRessource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketCorsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.DeleteBucketCors(ctx, &s3.DeleteBucketCorsInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		if isBucketSubresourceAbsent(err, "NoSuchCORSConfiguration") {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket CORS configuration",
			"Could not delete CORS configuration: "+err.Error(),
		)
	}
}

func (r *bucketCorsRessource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}
