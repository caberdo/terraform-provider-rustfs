// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
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
	_ resource.Resource                = &BucketDurabilityResource{}
	_ resource.ResourceWithImportState = &BucketDurabilityResource{}
)

func NewBucketDurabilityResource() resource.Resource {
	return &BucketDurabilityResource{}
}

type BucketDurabilityResource struct {
	client *AllClient
}

type BucketDurabilityResourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Mode   types.String `tfsdk:"mode"`
}

func (r *BucketDurabilityResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_durability"
}

func (r *BucketDurabilityResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage per-bucket durability settings in rustfs",
		MarkdownDescription: "Manage the per-bucket durability override in rustfs",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"mode": schema.StringAttribute{
				Required:    true,
				Description: "Durability override for the bucket: strict, relaxed or none. When unset the bucket inherits the process-wide durability mode.",
				Validators: []validator.String{
					stringvalidator.OneOf("strict", "relaxed", "none"),
				},
			},
		},
	}
}

func (r *BucketDurabilityResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *BucketDurabilityResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan BucketDurabilityResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := rustfs.BucketDurability{Bucket: plan.Bucket.ValueString(), Mode: plan.Mode.ValueString()}
	read, err := r.client.RustClient.SetBucketDurability(ctx, plan.Bucket.ValueString(), d)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating bucket durability",
			"Could not set bucket durability, unexpected error: "+err.Error(),
		)
		return
	}
	plan.Mode = types.StringValue(read.Mode)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BucketDurabilityResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state BucketDurabilityResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	read, err := r.client.RustClient.GetBucketDurability(ctx, state.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading bucket durability",
			"Could not read bucket durability, unexpected error: "+err.Error(),
		)
		return
	}
	// RustFS answers 200 with a null mode when the bucket has no durability
	// override, and it answers the same for a bucket that no longer exists
	// (probed live: GET bucket-durability/tf-probe-missing returns
	// {"bucket":"tf-probe-missing","mode":null}). Either way the override this
	// resource manages is gone, so drop it from state rather than write an
	// empty mode into a required attribute.
	if read.Mode == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Mode = types.StringValue(read.Mode)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketDurabilityResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan BucketDurabilityResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := rustfs.BucketDurability{Bucket: plan.Bucket.ValueString(), Mode: plan.Mode.ValueString()}
	read, err := r.client.RustClient.SetBucketDurability(ctx, plan.Bucket.ValueString(), d)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket durability",
			"Could not update bucket durability, unexpected error: "+err.Error(),
		)
		return
	}
	plan.Mode = types.StringValue(read.Mode)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BucketDurabilityResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketDurabilityResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RustClient.DeleteBucketDurability(ctx, data.Bucket.ValueString()); err != nil {
		// A bucket that no longer exists has no override left to remove:
		// RustFS answers 500 InternalError "failed to capture bucket
		// incarnation: Bucket not found: <bucket>" for the delete (probed
		// live). That is the endpoint's own missing-bucket text, so destroying
		// an already gone bucket is not an error; any other InternalError is
		// still reported.
		if rustfs.IsAPIErrorCode(err, "InternalError") && strings.Contains(err.Error(), "Bucket not found") {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket durability",
			"Could not delete bucket durability, unexpected error: "+err.Error(),
		)
	}
}

func (r *BucketDurabilityResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

func init() {
	registerResource(NewBucketDurabilityResource)
}
