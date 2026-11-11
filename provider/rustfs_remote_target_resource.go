// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
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
		Description: "Manage remote targets used as replication destinations or notification ARNs in rustfs",
		MarkdownDescription: "Manage remote targets used as replication destinations or notification ARNs in rustfs.\n\n" +
			"RustFS refuses to change the endpoint, credentials or TLS settings of a target that belongs to a " +
			"site-replication peer: such a target records the peer's deployment id and follows the site topology. " +
			"Those settings are edited through `rustfs_site_replication` instead; this resource keeps managing " +
			"ordinary targets. A peer-owned target can still be read, but an in-place update of it fails with a " +
			"diagnostic naming the peer deployment id (or the peer segment of its ARN) before any request is sent.",
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

	// arn is Computed, so a normal create carries none and the server assigns
	// one. A plan that already holds an ARN can only come from a create-after-
	// import path; such a plan must not try to rewrite a peer-owned target
	// either.
	if arn := plan.Arn.ValueString(); arn != "" {
		if peer := r.peerDeploymentID(ctx, plan.Bucket.ValueString(), arn); peer != "" {
			addPeerOwnedTargetDiagnostic(&resp.Diagnostics, plan.Bucket.ValueString(), arn, peer)
			return
		}
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

	// RustFS 1.0.5 rejects a connection-settings update on a target that
	// belongs to a site-replication peer, and its 400 says the target records a
	// peer deployment id (rustfs/rustfs#8814). Detect that from the listing
	// before sending, so the apply fails with an actionable diagnostic that
	// names the peer and the resource that can edit it. When the listing cannot
	// prove peer ownership (no target record, or the list call failed) the
	// update proceeds and the server's own error remains the fallback.
	if peer := r.peerDeploymentID(ctx, plan.Bucket.ValueString(), target.Arn); peer != "" {
		addPeerOwnedTargetDiagnostic(&resp.Diagnostics, plan.Bucket.ValueString(), target.Arn, peer)
		return
	}

	if _, err := r.client.RustClient.AddRemoteTarget(ctx, plan.Bucket.ValueString(), target); err != nil {
		resp.Diagnostics.AddError("Error updating remote target", err.Error())
		return
	}
	plan.Arn = state.Arn
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// peerDeploymentID returns the site-replication peer deployment id the server
// records for arn on bucket, or "" when the target is not peer-owned. It is
// also empty when the target is not in the listing or the listing itself
// failed, which keeps the server authoritative for the update instead of
// inventing ownership evidence.
func (r *RemoteTargetResource) peerDeploymentID(ctx context.Context, bucket, arn string) string {
	if bucket == "" || arn == "" {
		return ""
	}
	targets, err := r.client.RustClient.ListRemoteTargets(ctx, bucket)
	if err != nil {
		tflog.Debug(ctx, "could not inspect remote target ownership; leaving the decision to the server", map[string]any{
			"bucket": bucket,
			"arn":    arn,
			"error":  err.Error(),
		})
		return ""
	}
	for i := range targets {
		if targets[i].Arn == arn {
			return targets[i].PeerDeploymentID()
		}
	}
	return ""
}

// addPeerOwnedTargetDiagnostic explains why a peer-owned target cannot be
// changed through rustfs_remote_target.
func addPeerOwnedTargetDiagnostic(diags *diag.Diagnostics, bucket, arn, peer string) {
	diags.AddError(
		"Remote target is owned by site replication",
		fmt.Sprintf(
			"The target %s on bucket %s belongs to site-replication peer %q (the deployment id the target "+
				"records, or the peer segment of its ARN), so its endpoint, credentials and TLS settings follow "+
				"the site topology. RustFS refuses to change them through the remote-target route, and Terraform "+
				"did not send this update. Edit the peer with rustfs_site_replication (endpoint, access_key, "+
				"secret_key, skip_tls_verify, ca_cert_pem) instead.",
			arn, bucket, peer,
		),
	)
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
	if resp.Diagnostics.HasError() {
		return
	}

	// An imported target may belong to a site-replication peer. Import itself is
	// read-only, so it succeeds, but the user has to know that endpoint,
	// credential and TLS changes will be refused and that rustfs_site_replication
	// owns that configuration. A listing failure is not an error here: Read runs
	// next and reports the real problem.
	if peer := r.peerDeploymentID(ctx, parts[0], parts[1]); peer != "" {
		resp.Diagnostics.AddWarning(
			"Imported remote target is owned by site replication",
			fmt.Sprintf(
				"The target %s on bucket %s belongs to site-replication peer %q (the deployment id the target "+
					"records, or the peer segment of its ARN). Its endpoint, credentials and TLS settings follow "+
					"the site topology, so rustfs_remote_target cannot change them: edit the peer with "+
					"rustfs_site_replication instead.",
				parts[1], parts[0], peer,
			),
		)
	}
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
