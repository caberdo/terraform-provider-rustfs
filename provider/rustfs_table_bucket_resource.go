// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &TableBucketResource{}
	_ resource.ResourceWithImportState = &TableBucketResource{}
)

func NewTableBucketResource() resource.Resource {
	return &TableBucketResource{}
}

// TableBucketResource manages the S3 Tables (Iceberg REST catalog) switch on an
// existing bucket.
type TableBucketResource struct {
	client *AllClient
}

type TableBucketResourceModel struct {
	Bucket              types.String `tfsdk:"bucket"`
	Enabled             types.Bool   `tfsdk:"enabled"`
	CatalogType         types.String `tfsdk:"catalog_type"`
	Warehouse           types.String `tfsdk:"warehouse"`
	CatalogEntryPresent types.Bool   `tfsdk:"catalog_entry_present"`
	CatalogURI          types.String `tfsdk:"catalog_uri"`
}

func (r *TableBucketResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_table_bucket"
}

func (r *TableBucketResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage the S3 Tables catalog switch of a rustfs bucket",
		MarkdownDescription: "Manage the S3 Tables (Iceberg REST catalog) switch of an existing rustfs bucket.\n\n" +
			"Enabling the catalog marks the bucket as a table bucket served at `catalog_uri`. " +
			"Destroying this resource only switches the catalog off: the bucket and every object in it " +
			"are left untouched, but the bucket returns to ordinary lifecycle behaviour (table buckets " +
			"are excluded from normal expiration).\n\n" +
			"S3 Tables is a preview feature upstream. This resource exposes the switch, not Iceberg " +
			"client compatibility.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the existing bucket to enable the S3 Tables catalog on.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"enabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the S3 Tables catalog is enabled on the bucket.",
			},
			"catalog_type": schema.StringAttribute{
				Computed:    true,
				Description: "Catalog type the server runs for the bucket (iceberg-rest).",
			},
			"warehouse": schema.StringAttribute{
				Computed:    true,
				Description: "Warehouse the table catalog uses, as reported by the server.",
			},
			"catalog_entry_present": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the server holds a catalog entry for the bucket.",
			},
			"catalog_uri": schema.StringAttribute{
				Computed:    true,
				Description: "REST catalog URI the server serves for the bucket, for example /iceberg/v1/<bucket>.",
			},
		},
	}
}

func (r *TableBucketResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TableBucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TableBucketResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := plan.Bucket.ValueString()
	tableBucket, err := r.client.RustClient.EnableTableBucket(ctx, bucket)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating table bucket",
			"Could not enable the S3 Tables catalog for bucket "+bucket+": "+err.Error(),
		)
		return
	}
	// Read drops the resource whenever the switch is off, so writing an off
	// switch to state here would churn create/refresh/recreate forever. Fail
	// the apply instead of hiding the server's refusal.
	if !tableBucket.Enabled {
		resp.Diagnostics.AddError(
			"Error creating table bucket",
			"The S3 Tables catalog for bucket "+bucket+" is still disabled after the enable request.",
		)
		return
	}

	applyTableBucket(&plan, tableBucket)
	tflog.Trace(ctx, "enabled the S3 Tables catalog on a bucket", map[string]any{"bucket": bucket})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TableBucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TableBucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := state.Bucket.ValueString()
	tableBucket, err := r.client.RustClient.GetTableBucket(ctx, bucket)
	if err != nil {
		// RustFS 1.0.1 answers 400 "Config not found" for a bucket that no
		// longer exists; 1.0.2+ answer 200 with the switch off, handled below.
		if tableBucketConfigGone(err) {
			tflog.Trace(ctx, "table bucket catalog state no longer exists", map[string]any{"bucket": bucket})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading table bucket",
			"Could not read the S3 Tables catalog for bucket "+bucket+": "+err.Error(),
		)
		return
	}

	// The switch is the resource: when it is off (cleared outside Terraform, or
	// the bucket is gone on 1.0.2+) there is nothing left to manage.
	if !tableBucket.Enabled {
		tflog.Trace(ctx, "table bucket catalog switch is off", map[string]any{"bucket": bucket})
		resp.State.RemoveResource(ctx)
		return
	}

	applyTableBucket(&state, tableBucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TableBucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TableBucketResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Every configurable attribute is RequiresReplace, so Terraform plans a
	// replacement instead of calling this method. Writing the plan back keeps
	// the state consistent if the framework invokes it anyway.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TableBucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TableBucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := state.Bucket.ValueString()
	if _, err := r.client.RustClient.DisableTableBucket(ctx, bucket); err != nil {
		// RustFS 1.0.1 registers the disable route but answers 501
		// NotImplemented. Destroy cannot succeed there, so remove the resource
		// from state with a warning instead of failing every run.
		if rustfs.IsUnsupportedFeature(err) {
			resp.Diagnostics.AddWarning(
				"S3 Tables catalog switch left enabled",
				"The table bucket was removed from Terraform state, but this server could not switch "+
					"the catalog off: "+err.Error()+". The catalog stays enabled on the bucket until it "+
					"is disabled on a server that implements the route.",
			)
			return
		}
		// A bucket that no longer exists has no catalog switch left to turn
		// off. 1.0.2+ answer 500 RESTException "Bucket not found" here.
		if tableBucketMissing(err) {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting table bucket",
			"Could not disable the S3 Tables catalog for bucket "+bucket+": "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "disabled the S3 Tables catalog on a bucket", map[string]any{"bucket": bucket})
}

func (r *TableBucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

// applyTableBucket copies the catalog state the server reported into the model.
func applyTableBucket(model *TableBucketResourceModel, tableBucket rustfs.TableBucket) {
	model.Enabled = types.BoolValue(tableBucket.Enabled)
	model.CatalogType = types.StringValue(tableBucket.CatalogType)
	model.Warehouse = types.StringValue(tableBucket.Warehouse)
	model.CatalogEntryPresent = types.BoolValue(tableBucket.CatalogEntryPresent)
	model.CatalogURI = types.StringValue(tableBucket.CatalogURI)
}

// tableBucketConfigGone reports whether err is RustFS 1.0.1's answer for a
// bucket whose table-bucket metadata cannot be loaded. The endpoint has no
// structured code for it, so this matches its own wording, and only on the
// status that carries it.
func tableBucketConfigGone(err error) bool {
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusBadRequest && strings.Contains(apiErr.Error(), "Config not found")
}

// tableBucketMissing reports whether err is the server's answer that the bucket
// itself is gone (the disable route has no structured code for it either).
func tableBucketMissing(err error) bool {
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusInternalServerError && strings.Contains(apiErr.Error(), "Bucket not found")
}

func init() {
	registerResource(NewTableBucketResource)
}
