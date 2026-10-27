// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var (
	_ resource.Resource                = &SiteReplicationResyncResource{}
	_ resource.ResourceWithConfigure   = &SiteReplicationResyncResource{}
	_ resource.ResourceWithImportState = &SiteReplicationResyncResource{}
)

// SiteReplicationResyncResource drives one site-replication resync operation
// and records the server's answer in state.
//
// It is an action, not managed state: RustFS has no route that reads an
// operation back by id, so Read echoes the recorded result and Delete is a
// no-op. Destroy deliberately does not cancel the resync; cancelling is the
// explicit `operation = "cancel"` apply.
type SiteReplicationResyncResource struct {
	client *AllClient
}

type SiteReplicationResyncResourceModel struct {
	ID        types.String `tfsdk:"id"`
	Operation types.String `tfsdk:"operation"`
	Peer      types.Object `tfsdk:"peer"`
	Triggers  types.Map    `tfsdk:"triggers"`
	ResyncID  types.String `tfsdk:"resync_id"`
	OpType    types.String `tfsdk:"op_type"`
	Status    types.String `tfsdk:"status"`
	State     types.String `tfsdk:"state"`
	Message   types.String `tfsdk:"message"`
}

type siteReplicationResyncPeerModel struct {
	Name         types.String `tfsdk:"name"`
	Endpoint     types.String `tfsdk:"endpoint"`
	DeploymentID types.String `tfsdk:"deployment_id"`
}

var siteReplicationResyncPeerAttrTypes = map[string]attr.Type{
	"name":          types.StringType,
	"endpoint":      types.StringType,
	"deployment_id": types.StringType,
}

func NewSiteReplicationResyncResource() resource.Resource {
	return &SiteReplicationResyncResource{}
}

func (r *SiteReplicationResyncResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_replication_resync"
}

func (r *SiteReplicationResyncResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	description := "Start, cancel or inspect a RustFS site-replication resync " +
		"(PUT /rustfs/admin/v3/site-replication/resync/op).\n\n" +
		"**A resync re-copies data between sites.** It walks the replicated buckets and depends on the " +
		"server's existing site-replication configuration, so it can move a large amount of data. " +
		"Every operation names the target peer by `deployment_id`; `operation = \"status\"` only reads " +
		"that peer's current operation.\n\n" +
		"Every configurable attribute carries RequiresReplace: changing `operation`, `peer`, a peer field " +
		"or `triggers` replaces the resource and runs a new operation instead of editing the recorded " +
		"one, so `Update` never edits in place.\n\n" +
		"Destroying this resource does nothing on the server. The provider does not cancel a resync on " +
		"destroy: cancelling must be an explicit `operation = \"cancel\"` apply, because a resync is " +
		"operator-driven and a `terraform destroy` should not silently change replication state. Read only " +
		"echoes the recorded result, since the server has no neutral read by id.\n\n" +
		"Read `rustfs_site_replication_status` first to get each peer's `deployment_id`."
	resp.Schema = schema.Schema{
		Description:         "Run a RustFS site-replication resync operation",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				Description: "Resource identifier: the resync id the server returned, or " +
					"`<operation>/<deployment_id>` when the server returned none.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"operation": schema.StringAttribute{
				Required: true,
				Description: "Operation to run: `start` re-copies data to the peer, `cancel` stops the peer's " +
					"current resync, `status` reads it without changing anything. Changing it replaces the " +
					"resource and runs the new operation.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("start", "cancel", "status"),
				},
			},
			"peer": schema.SingleNestedAttribute{
				Required: true,
				Description: "Target peer. Every operation requires `deployment_id`, which the server uses to " +
					"identify the peer. Changing the block replaces the resource and runs the operation " +
					"against the new peer.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Optional: true,
						Description: "Peer site name. Informational: the server identifies the peer by " +
							"`deployment_id`.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"endpoint": schema.StringAttribute{
						Optional: true,
						Description: "Peer admin endpoint. Informational: the server identifies the peer by " +
							"`deployment_id`.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"deployment_id": schema.StringAttribute{
						Required: true,
						Description: "Deployment ID of the peer, as reported by " +
							"`rustfs_site_replication_status`. Every operation needs it: the server " +
							"identifies the peer by deployment ID and refuses the request without it. " +
							"Changing it replaces the resource.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
						},
					},
				},
			},
			"triggers": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Arbitrary values that are not sent to the server. Change one to run the operation " +
					"again; without a change Terraform does not repeat it.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"resync_id": schema.StringAttribute{
				Computed:    true,
				Description: "Resync operation id the server returned; empty when the server returned none.",
			},
			"op_type": schema.StringAttribute{
				Computed:    true,
				Description: "Operation type the server echoed.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Status the server returned for the operation.",
			},
			"state": schema.StringAttribute{
				Computed:    true,
				Description: "State the server returned for the operation, when it reports one.",
			},
			"message": schema.StringAttribute{
				Computed: true,
				Description: "Error detail the server returned, empty when the operation did not fail. A " +
					"`status = \"failed\"` result is not a provider error: the apply records what the server " +
					"answered.",
			},
		},
	}
}

func (r *SiteReplicationResyncResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *SiteReplicationResyncResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SiteReplicationResyncResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.applyResync(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SiteReplicationResyncResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SiteReplicationResyncResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// RustFS has no route that reports a resync operation by id, and the action
	// is not undone by anything the provider could observe, so Read keeps the
	// recorded answer instead of inventing one.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SiteReplicationResyncResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SiteReplicationResyncResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Every configurable attribute carries RequiresReplace, so Update only runs
	// for an unplanned diff. Still execute rather than silently keeping state.
	r.applyResync(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SiteReplicationResyncResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// Destroy never cancels a resync: cancellation is an explicit
	// operation = "cancel" apply so a terraform destroy cannot change
	// replication state behind the operator's back.
}

func (r *SiteReplicationResyncResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// An imported resync cannot be read back from the server. Record the import
	// id as the resync id so a later plan shows the recorded operation; the
	// configuration still supplies operation and peer.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *SiteReplicationResyncResource) applyResync(
	ctx context.Context,
	plan *SiteReplicationResyncResourceModel,
	diagnostics *diag.Diagnostics,
) {
	if plan.Operation.IsUnknown() || plan.Operation.IsNull() {
		diagnostics.AddError(
			"Incomplete site replication resync",
			"operation must be resolved before the resync request can be sent.",
		)
		return
	}
	if plan.Peer.IsUnknown() || plan.Peer.IsNull() {
		diagnostics.AddError(
			"Incomplete site replication resync",
			"peer must be resolved before the resync request can be sent.",
		)
		return
	}

	var peer siteReplicationResyncPeerModel
	if diags := plan.Peer.As(ctx, &peer, basetypes.ObjectAsOptions{}); diags.HasError() {
		diagnostics.Append(diags...)
		return
	}

	operation := plan.Operation.ValueString()
	result, err := r.client.RustClient.SiteReplicationResyncOp(ctx, operation, rustfs.SiteReplicationPeer{
		Name:         peer.Name.ValueString(),
		Endpoint:     peer.Endpoint.ValueString(),
		DeploymentID: peer.DeploymentID.ValueString(),
	})
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			diagnostics.AddError(
				"Site replication resync is not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_site_replication_resync resource from the configuration.",
			)
			return
		}
		diagnostics.AddError(
			"Error running site replication resync",
			"Could not run site replication resync operation "+operation+": "+err.Error(),
		)
		return
	}

	id := result.ID
	if id == "" {
		id = operation + "/" + peer.DeploymentID.ValueString()
	}

	plan.ID = types.StringValue(id)
	plan.ResyncID = types.StringValue(result.ID)
	plan.OpType = types.StringValue(result.OpType)
	plan.Status = types.StringValue(result.Status)
	plan.State = types.StringValue(result.State)
	plan.Message = types.StringValue(result.ErrDetail)
}

func init() {
	registerResource(NewSiteReplicationResyncResource)
}
