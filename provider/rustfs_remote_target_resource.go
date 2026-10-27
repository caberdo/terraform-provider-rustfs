// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &RemoteTargetResource{}
	_ resource.ResourceWithImportState = &RemoteTargetResource{}
)

// RemoteTargetResource manages remote targets used as replication destinations
// or notification ARNs in rustfs.
type RemoteTargetResource struct {
	client *AllClient
}

type RemoteTargetResourceModel struct {
	Arn          types.String `tfsdk:"arn"`
	Type         types.String `tfsdk:"type"`
	Endpoint     types.String `tfsdk:"endpoint"`
	AccessKey    types.String `tfsdk:"access_key"`
	SecretKey    types.String `tfsdk:"secret_key"`
	Secure       types.Bool   `tfsdk:"secure"`
	Region       types.String `tfsdk:"region"`
	Path         types.String `tfsdk:"path"`
	Bucket       types.String `tfsdk:"bucket"`
	TargetBucket types.String `tfsdk:"target_bucket"`
}

// NewRemoteTargetResource returns a new RemoteTargetResource.
func NewRemoteTargetResource() resource.Resource {
	return &RemoteTargetResource{}
}

func (r *RemoteTargetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_remote_target"
}

func (r *RemoteTargetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage remote targets used as replication destinations or notification ARNs in rustfs",
		MarkdownDescription: "Manage remote targets used as replication destinations or notification ARNs in rustfs",
		Attributes: map[string]schema.Attribute{
			"arn": schema.StringAttribute{
				Computed:    true,
				Description: "ARN assigned by the server to the remote target.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("replication"),
				Description: "Remote target type. Only 'replication' is supported by this server version.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"endpoint": schema.StringAttribute{
				Required:    true,
				Description: "Endpoint of the remote target, reachable from the rustfs server.",
			},
			"access_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Access key for the remote target.",
			},
			"secret_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Secret key for the remote target.",
			},
			"secure": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Use TLS for the remote target connection. Defaults to true.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Description: "Region of the remote target.",
				// The server embeds the region in the target ARN and ignores it on
				// update, so a region change has to create a new target.
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"path": schema.StringAttribute{
				Optional:    true,
				Description: "Path prefix on the remote target.",
				// Path is only applied through the server's `path` update group,
				// which this client does not send, so a path change has to create
				// a new target.
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Source bucket on which the remote target is registered.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"target_bucket": schema.StringAttribute{
				Required:    true,
				Description: "Destination bucket on the remote target.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *RemoteTargetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *RemoteTargetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan RemoteTargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	arn, err := r.client.RustClient.AddRemoteTarget(ctx, plan.Bucket.ValueString(), r.targetFromModel(plan))
	if err != nil {
		resp.Diagnostics.AddError("Error creating remote target", err.Error())
		return
	}
	plan.Arn = types.StringValue(arn)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *RemoteTargetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state RemoteTargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	targets, err := r.client.RustClient.ListRemoteTargets(ctx, state.Bucket.ValueString())
	if err != nil {
		// Live evidence (RustFS latest, 2026-10-19): list-remote-targets on a
		// bucket that does not exist answers 404 NoSuchBucket. A source bucket
		// that is gone cannot hold a remote target, so the target is gone too
		// and the refresh must plan a recreation instead of failing.
		if rustfs.IsAPIErrorCode(err, "NoSuchBucket") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error listing remote targets", err.Error())
		return
	}
	var found *rustfs.RemoteTarget
	for i := range targets {
		if targets[i].Arn == state.Arn.ValueString() {
			found = &targets[i]
			break
		}
	}
	if found == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Arn = types.StringValue(found.Arn)
	state.Type = types.StringValue(found.Type)
	state.Endpoint = types.StringValue(found.Endpoint)
	state.Secure = types.BoolValue(found.Secure)
	if found.Region != "" {
		state.Region = types.StringValue(found.Region)
	} else {
		state.Region = types.StringNull()
	}
	if found.Path != "" {
		state.Path = types.StringValue(found.Path)
	} else {
		state.Path = types.StringNull()
	}
	state.TargetBucket = types.StringValue(found.TargetBucket)
	if found.Credentials != nil {
		state.AccessKey = types.StringValue(found.Credentials.AccessKey)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *RemoteTargetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state RemoteTargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	target := r.targetFromModel(plan)
	target.Arn = state.Arn.ValueString()
	if _, err := r.client.RustClient.AddRemoteTarget(ctx, plan.Bucket.ValueString(), target); err != nil {
		resp.Diagnostics.AddError("Error updating remote target", err.Error())
		return
	}
	plan.Arn = state.Arn
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *RemoteTargetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data RemoteTargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A replication configuration that references the target removes it as
	// part of its own deletion, so the target may already be gone by the time
	// Terraform destroys this resource; that is not an error.
	if err := r.client.RustClient.DeleteRemoteTarget(ctx, data.Bucket.ValueString(), data.Arn.ValueString()); err != nil {
		// Two ways the target is already gone: a removal that reports the
		// target missing (a replication configuration deletes the targets it
		// references), or a source bucket that no longer exists, which the
		// admin API answers with 404 NoSuchBucket (probed against RustFS
		// latest, 2026-10-19).
		if isRemoteTargetNotFound(err) || rustfs.IsAPIErrorCode(err, "NoSuchBucket") {
			return
		}
		resp.Diagnostics.AddError("Error deleting remote target", err.Error())
	}
}

// isRemoteTargetNotFound reports whether the admin API refused the removal
// because the target no longer exists.
func isRemoteTargetNotFound(err error) bool {
	return strings.Contains(err.Error(), "Remote target not found") ||
		strings.Contains(err.Error(), "BucketRemoteTargetNotFound")
}

func (r *RemoteTargetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import ID is the composite "<source-bucket>:<arn>".
	id := req.ID
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			"Expected import ID in the format <source-bucket>:<arn>",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("arn"), parts[1])...)
}

func (r *RemoteTargetResource) targetFromModel(plan RemoteTargetResourceModel) rustfs.RemoteTarget {
	return rustfs.RemoteTarget{
		Type:         plan.Type.ValueString(),
		Endpoint:     plan.Endpoint.ValueString(),
		Secure:       plan.Secure.ValueBool(),
		Region:       plan.Region.ValueString(),
		Path:         plan.Path.ValueString(),
		TargetBucket: plan.TargetBucket.ValueString(),
		Credentials: &rustfs.RemoteTargetCredentials{
			AccessKey: plan.AccessKey.ValueString(),
			SecretKey: plan.SecretKey.ValueString(),
		},
	}
}

func init() {
	registerResource(NewRemoteTargetResource)
}
