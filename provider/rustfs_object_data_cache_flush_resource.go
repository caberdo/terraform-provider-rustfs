// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &ObjectDataCacheFlushResource{}
	_ resource.ResourceWithValidateConfig = &ObjectDataCacheFlushResource{}
)

// ObjectDataCacheFlushResource performs an operator-driven object data cache
// flush. It is an action, not managed state: Read echoes the recorded result and
// Delete is a no-op. A trigger or scope change runs the flush again.
type ObjectDataCacheFlushResource struct {
	client *AllClient
}

type ObjectDataCacheFlushResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Triggers    types.Map    `tfsdk:"triggers"`
	Bucket      types.String `tfsdk:"bucket"`
	Object      types.String `tfsdk:"object"`
	Scope       types.String `tfsdk:"scope"`
	Outcome     types.String `tfsdk:"outcome"`
	RemovedKeys types.Int64  `tfsdk:"removed_keys"`
}

func NewObjectDataCacheFlushResource() resource.Resource {
	return &ObjectDataCacheFlushResource{}
}

func (r *ObjectDataCacheFlushResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_data_cache_flush"
}

func (r *ObjectDataCacheFlushResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	description := "Flush the RustFS object data cache (POST /rustfs/admin/v3/object-data-cache/flush). " +
		"Without `bucket` the whole cache is flushed; with `bucket` only that bucket; with `bucket` and " +
		"`object` only that identity. The action is one-shot: Read only echoes the recorded result, Delete " +
		"is a no-op, and changing `triggers`, `bucket` or `object` runs the flush again. Cache tuning stays " +
		"in the server configuration."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Flush trigger identifier.",
			},
			"triggers": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Arbitrary values that are not sent to the server. Change one to run the flush again; " +
					"without a change Terraform does not repeat it.",
			},
			"bucket": schema.StringAttribute{
				Optional:    true,
				Description: "Bucket whose cached objects are flushed. Omit to flush the whole cache.",
			},
			"object": schema.StringAttribute{
				Optional:    true,
				Description: "Object identity to flush. Requires `bucket`.",
			},
			"scope": schema.StringAttribute{
				Computed:    true,
				Description: "Scope the server applied: `all`, `bucket` or `object`.",
			},
			"outcome": schema.StringAttribute{
				Computed:    true,
				Description: "Outcome the server reported: `removed` when entries were dropped, `noop` otherwise.",
			},
			"removed_keys": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of cache entries the flush removed.",
			},
		},
	}
}

func (r *ObjectDataCacheFlushResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ObjectDataCacheFlushResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config ObjectDataCacheFlushResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.Object.IsNull() || config.Object.IsUnknown() || config.Object.ValueString() == "" {
		return
	}
	if config.Bucket.IsNull() || config.Bucket.IsUnknown() || config.Bucket.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("object"),
			"Invalid object data cache flush",
			"object requires bucket: the server rejects an object flush without a bucket.",
		)
	}
}

func (r *ObjectDataCacheFlushResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ObjectDataCacheFlushResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.runFlush(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ObjectDataCacheFlushResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ObjectDataCacheFlushResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ObjectDataCacheFlushResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ObjectDataCacheFlushResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.runFlush(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ObjectDataCacheFlushResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *ObjectDataCacheFlushResource) runFlush(
	ctx context.Context,
	model *ObjectDataCacheFlushResourceModel,
	diagnostics *diag.Diagnostics,
) {
	result, err := r.client.RustClient.FlushObjectDataCache(ctx, model.Bucket.ValueString(), model.Object.ValueString())
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			diagnostics.AddError(
				"Object data cache flush is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_object_data_cache_flush resource from the configuration.",
			)
			return
		}
		diagnostics.AddError("Error flushing object data cache", "Could not flush object data cache: "+err.Error())
		return
	}

	model.ID = types.StringValue("object-data-cache-flush")
	model.Scope = types.StringValue(result.Scope)
	model.Outcome = types.StringValue(result.Outcome)
	model.RemovedKeys = types.Int64Value(int64(result.RemovedKeys))
}

func init() {
	registerResource(NewObjectDataCacheFlushResource)
}
