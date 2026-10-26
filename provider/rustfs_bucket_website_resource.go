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
	_ resource.Resource                = &bucketWebsiteResource{}
	_ resource.ResourceWithImportState = &bucketWebsiteResource{}
)

// NewBucketWebsiteResource manages the static-website configuration of a
// bucket.
func NewBucketWebsiteResource() resource.Resource {
	return &bucketWebsiteResource{}
}

type bucketWebsiteResource struct {
	client *AllClient
}

type bucketWebsiteModel struct {
	Bucket                types.String                `tfsdk:"bucket"`
	Id                    types.String                `tfsdk:"id"`
	IndexDocumentSuffix   types.String                `tfsdk:"index_document_suffix"`
	ErrorDocumentKey      types.String                `tfsdk:"error_document_key"`
	RedirectAllRequestsTo *redirectAllRequestsToModel `tfsdk:"redirect_all_requests_to"`
	RoutingRule           []routingRuleModel          `tfsdk:"routing_rule"`
}

type redirectAllRequestsToModel struct {
	HostName types.String `tfsdk:"host_name"`
	Protocol types.String `tfsdk:"protocol"`
}

type routingRuleModel struct {
	Condition *routingRuleConditionModel `tfsdk:"condition"`
	Redirect  *routingRuleRedirectModel  `tfsdk:"redirect"`
}

type routingRuleConditionModel struct {
	KeyPrefixEquals             types.String `tfsdk:"key_prefix_equals"`
	HttpErrorCodeReturnedEquals types.String `tfsdk:"http_error_code_returned_equals"`
}

type routingRuleRedirectModel struct {
	HostName             types.String `tfsdk:"host_name"`
	HttpRedirectCode     types.String `tfsdk:"http_redirect_code"`
	Protocol             types.String `tfsdk:"protocol"`
	ReplaceKeyPrefixWith types.String `tfsdk:"replace_key_prefix_with"`
	ReplaceKeyWith       types.String `tfsdk:"replace_key_with"`
}

func (r *bucketWebsiteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_website"
}

func (r *bucketWebsiteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage the website configuration of an S3 bucket in rustfs",
		MarkdownDescription: "Manage the static-website configuration of a bucket: index and error documents, a " +
			"redirect for every request, and routing rules. Destroying this resource removes the website configuration " +
			"(`DeleteBucketWebsite`); the bucket and its objects stay. A bucket with no website configuration is " +
			"removed from state on refresh rather than reported as drift.",
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
			"index_document_suffix": schema.StringAttribute{
				Optional: true,
				Description: "Object name suffix served for a request that ends at a directory (for example `index.html`). " +
					"Required unless `redirect_all_requests_to` is set.",
			},
			"error_document_key": schema.StringAttribute{
				Optional:    true,
				Description: "Object key served for a 4XX error (for example `error.html`).",
			},
			"redirect_all_requests_to": schema.SingleNestedAttribute{
				Optional: true,
				Description: "Redirect every request to another host. This cannot be combined with index/error " +
					"documents or routing rules.",
				Attributes: map[string]schema.Attribute{
					"host_name": schema.StringAttribute{
						Required:    true,
						Description: "Host to redirect to, without a protocol (for example `example.com`).",
					},
					"protocol": schema.StringAttribute{
						Optional:    true,
						Description: "Protocol to redirect to: http or https.",
						Validators: []validator.String{
							stringvalidator.OneOf(enumStrings(s3types.Protocol("").Values())...),
						},
					},
				},
			},
		},
		Blocks: map[string]schema.Block{
			"routing_rule": schema.ListNestedBlock{
				Description: "Redirect rules evaluated in order.",
				NestedObject: schema.NestedBlockObject{
					Blocks: map[string]schema.Block{
						"condition": schema.SingleNestedBlock{
							Description: "Request condition the rule matches. With no condition the rule matches every request.",
							Attributes: map[string]schema.Attribute{
								"key_prefix_equals": schema.StringAttribute{
									Optional:    true,
									Description: "Match when the object key starts with this prefix.",
								},
								"http_error_code_returned_equals": schema.StringAttribute{
									Optional:    true,
									Description: "Match when the request fails with this HTTP status code.",
								},
							},
						},
						"redirect": schema.SingleNestedBlock{
							Description: "Where a matching request is redirected.",
							Attributes: map[string]schema.Attribute{
								"host_name": schema.StringAttribute{
									Optional:    true,
									Description: "Host to redirect to.",
								},
								"http_redirect_code": schema.StringAttribute{
									Optional:    true,
									Description: "HTTP redirect status code (for example 301).",
								},
								"protocol": schema.StringAttribute{
									Optional:    true,
									Description: "Protocol to redirect to: http or https.",
									Validators: []validator.String{
										stringvalidator.OneOf(enumStrings(s3types.Protocol("").Values())...),
									},
								},
								"replace_key_prefix_with": schema.StringAttribute{
									Optional:    true,
									Description: "Replace the matched key prefix with this value.",
								},
								"replace_key_with": schema.StringAttribute{
									Optional:    true,
									Description: "Replace the whole object key with this value.",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *bucketWebsiteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// buildWebsiteConfiguration maps the planned model onto the S3 document. The
// server validates the combination (a redirect-all configuration cannot carry
// documents, and a website without an index document is rejected).
func buildWebsiteConfiguration(plan bucketWebsiteModel) *s3types.WebsiteConfiguration {
	configuration := &s3types.WebsiteConfiguration{}

	if v := plan.IndexDocumentSuffix.ValueString(); v != "" {
		configuration.IndexDocument = &s3types.IndexDocument{Suffix: aws.String(v)}
	}
	if v := plan.ErrorDocumentKey.ValueString(); v != "" {
		configuration.ErrorDocument = &s3types.ErrorDocument{Key: aws.String(v)}
	}
	if redirect := plan.RedirectAllRequestsTo; redirect != nil {
		configuration.RedirectAllRequestsTo = &s3types.RedirectAllRequestsTo{
			HostName: aws.String(redirect.HostName.ValueString()),
		}
		if v := redirect.Protocol.ValueString(); v != "" {
			configuration.RedirectAllRequestsTo.Protocol = s3types.Protocol(v)
		}
	}

	for _, rule := range plan.RoutingRule {
		out := s3types.RoutingRule{}
		if condition := rule.Condition; condition != nil {
			out.Condition = &s3types.Condition{}
			if v := condition.KeyPrefixEquals.ValueString(); v != "" {
				out.Condition.KeyPrefixEquals = aws.String(v)
			}
			if v := condition.HttpErrorCodeReturnedEquals.ValueString(); v != "" {
				out.Condition.HttpErrorCodeReturnedEquals = aws.String(v)
			}
		}
		if redirect := rule.Redirect; redirect != nil {
			out.Redirect = &s3types.Redirect{}
			if v := redirect.HostName.ValueString(); v != "" {
				out.Redirect.HostName = aws.String(v)
			}
			if v := redirect.HttpRedirectCode.ValueString(); v != "" {
				out.Redirect.HttpRedirectCode = aws.String(v)
			}
			if v := redirect.Protocol.ValueString(); v != "" {
				out.Redirect.Protocol = s3types.Protocol(v)
			}
			if v := redirect.ReplaceKeyPrefixWith.ValueString(); v != "" {
				out.Redirect.ReplaceKeyPrefixWith = aws.String(v)
			}
			if v := redirect.ReplaceKeyWith.ValueString(); v != "" {
				out.Redirect.ReplaceKeyWith = aws.String(v)
			}
		}
		configuration.RoutingRules = append(configuration.RoutingRules, out)
	}

	return configuration
}

// flattenWebsiteConfiguration mirrors a GetBucketWebsite answer into the model.
// Every branch sets a value, so a configuration removed out of band does not
// leave the previous run's fields in state.
func flattenWebsiteConfiguration(out *s3.GetBucketWebsiteOutput, model *bucketWebsiteModel) {
	model.IndexDocumentSuffix = types.StringNull()
	if out.IndexDocument != nil && out.IndexDocument.Suffix != nil {
		model.IndexDocumentSuffix = types.StringValue(*out.IndexDocument.Suffix)
	}

	model.ErrorDocumentKey = types.StringNull()
	if out.ErrorDocument != nil && out.ErrorDocument.Key != nil {
		model.ErrorDocumentKey = types.StringValue(*out.ErrorDocument.Key)
	}

	model.RedirectAllRequestsTo = nil
	if out.RedirectAllRequestsTo != nil {
		redirect := &redirectAllRequestsToModel{
			HostName: types.StringValue(aws.ToString(out.RedirectAllRequestsTo.HostName)),
			Protocol: types.StringNull(),
		}
		if out.RedirectAllRequestsTo.Protocol != "" {
			redirect.Protocol = types.StringValue(string(out.RedirectAllRequestsTo.Protocol))
		}
		model.RedirectAllRequestsTo = redirect
	}

	model.RoutingRule = make([]routingRuleModel, 0, len(out.RoutingRules))
	for _, rule := range out.RoutingRules {
		flattened := routingRuleModel{}
		if rule.Condition != nil {
			condition := &routingRuleConditionModel{
				KeyPrefixEquals:             types.StringNull(),
				HttpErrorCodeReturnedEquals: types.StringNull(),
			}
			if rule.Condition.KeyPrefixEquals != nil {
				condition.KeyPrefixEquals = types.StringValue(*rule.Condition.KeyPrefixEquals)
			}
			if rule.Condition.HttpErrorCodeReturnedEquals != nil {
				condition.HttpErrorCodeReturnedEquals = types.StringValue(*rule.Condition.HttpErrorCodeReturnedEquals)
			}
			flattened.Condition = condition
		}
		if rule.Redirect != nil {
			redirect := &routingRuleRedirectModel{
				HostName:             types.StringNull(),
				HttpRedirectCode:     types.StringNull(),
				Protocol:             types.StringNull(),
				ReplaceKeyPrefixWith: types.StringNull(),
				ReplaceKeyWith:       types.StringNull(),
			}
			if rule.Redirect.HostName != nil {
				redirect.HostName = types.StringValue(*rule.Redirect.HostName)
			}
			if rule.Redirect.HttpRedirectCode != nil {
				redirect.HttpRedirectCode = types.StringValue(*rule.Redirect.HttpRedirectCode)
			}
			if rule.Redirect.Protocol != "" {
				redirect.Protocol = types.StringValue(string(rule.Redirect.Protocol))
			}
			if rule.Redirect.ReplaceKeyPrefixWith != nil {
				redirect.ReplaceKeyPrefixWith = types.StringValue(*rule.Redirect.ReplaceKeyPrefixWith)
			}
			if rule.Redirect.ReplaceKeyWith != nil {
				redirect.ReplaceKeyWith = types.StringValue(*rule.Redirect.ReplaceKeyWith)
			}
			flattened.Redirect = redirect
		}
		model.RoutingRule = append(model.RoutingRule, flattened)
	}
}

func (r *bucketWebsiteResource) put(ctx context.Context, bucket string, configuration *s3types.WebsiteConfiguration) error {
	_, err := r.client.S3.PutBucketWebsite(ctx, &s3.PutBucketWebsiteInput{
		Bucket:               aws.String(bucket),
		WebsiteConfiguration: configuration,
	})
	return err
}

func (r *bucketWebsiteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketWebsiteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := plan.Bucket.ValueString()
	if err := r.put(ctx, bucket, buildWebsiteConfiguration(plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error creating bucket website configuration",
			"Could not create the website configuration: "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "created a bucket website configuration resource")

	plan.Id = types.StringValue(bucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketWebsiteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketWebsiteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.client.S3.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{
		Bucket: aws.String(state.Bucket.ValueString()),
	})
	if err != nil {
		if isBucketSubresourceAbsent(err, "NoSuchWebsiteConfiguration") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket website configuration",
			"Could not read the website configuration: "+err.Error(),
		)
		return
	}

	flattenWebsiteConfiguration(out, &state)
	state.Id = types.StringValue(state.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketWebsiteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketWebsiteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := plan.Bucket.ValueString()
	if err := r.put(ctx, bucket, buildWebsiteConfiguration(plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket website configuration",
			"Could not update the website configuration: "+err.Error(),
		)
		return
	}

	plan.Id = types.StringValue(bucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketWebsiteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketWebsiteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.DeleteBucketWebsite(ctx, &s3.DeleteBucketWebsiteInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		if isBucketSubresourceAbsent(err, "NoSuchWebsiteConfiguration") {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket website configuration",
			"Could not delete the website configuration: "+err.Error(),
		)
	}
}

func (r *bucketWebsiteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func init() {
	registerResource(NewBucketWebsiteResource)
}
