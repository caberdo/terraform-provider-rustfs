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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &SiteReplicationResource{}
	_ resource.ResourceWithImportState = &SiteReplicationResource{}
)

func NewSiteReplicationResource() resource.Resource {
	return &SiteReplicationResource{}
}

type SiteReplicationResource struct {
	client *AllClient
}

type SiteReplicationResourceModel struct {
	Name          types.String `tfsdk:"name"`
	Endpoint      types.String `tfsdk:"endpoint"`
	AccessKey     types.String `tfsdk:"access_key"`
	SecretKey     types.String `tfsdk:"secret_key"`
	SkipTLSVerify types.Bool   `tfsdk:"skip_tls_verify"`
	CACertPEM     types.String `tfsdk:"ca_cert_pem"`
}

func (r *SiteReplicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_replication"
}

func (r *SiteReplicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage site-replication peers in rustfs",
		MarkdownDescription: "Manage site-replication peers in rustfs.\n\n" +
			"Each resource manages one peer. Endpoint, credential and TLS changes edit the peer in place; " +
			"only `name` forces a replacement, so adding or removing one peer never recreates the others. " +
			"Use `rustfs_site_replication_repair` to reconcile a diverged group.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Unique name of the replication peer. Changing this forces recreation.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"endpoint": schema.StringAttribute{
				Required:    true,
				Description: "Endpoint of the peer site",
			},
			"access_key": schema.StringAttribute{
				Required:    true,
				Description: "Access key of the peer site",
			},
			"secret_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Secret key of the peer site",
			},
			"skip_tls_verify": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Skip TLS certificate verification when connecting to the peer. Defaults to false.",
			},
			"ca_cert_pem": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Custom CA certificate (PEM) for the peer connection",
			},
		},
	}
}

func (r *SiteReplicationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *SiteReplicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SiteReplicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := modelToSite(plan)
	if err := r.client.RustClient.SiteReplicationAdd(ctx, site); err != nil {
		resp.Diagnostics.AddError("Error creating site replication", "Could not create site replication: "+err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SiteReplicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SiteReplicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	info, err := r.client.RustClient.SiteReplicationInfo(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading site replication", "Could not read site replication: "+err.Error())
		return
	}
	var found *rustfs.SiteReplicationPeer
	for i := range info.Sites {
		if info.Sites[i].Name == state.Name.ValueString() {
			found = &info.Sites[i]
			break
		}
	}
	if found == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Endpoint = types.StringValue(found.Endpoint)
	state.SkipTLSVerify = types.BoolValue(found.SkipTLSVerify)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SiteReplicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SiteReplicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The edit route updates the local topology and fans the change out to the
	// remaining peers, so endpoint, credential and TLS changes land in place:
	// only `name` (the peer identity) forces a replacement.
	peer := modelToPeerEdit(plan)
	if err := r.client.RustClient.EditPeer(ctx, peer); err != nil {
		resp.Diagnostics.AddError("Error updating site replication", "Could not update site replication: "+err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SiteReplicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data SiteReplicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RustClient.RemovePeer(ctx, []string{data.Name.ValueString()}); err != nil {
		// A site that is not (or no longer) configured is already gone: RustFS
		// answers 400 InvalidRequest "none of the requested sites are
		// configured" for the public remove route when none of the requested
		// names is a configured peer. Both the structured code and the
		// endpoint's own missing-site text are required, so an unrelated
		// InvalidRequest is still reported.
		if rustfs.IsAPIErrorCode(err, "InvalidRequest") && strings.Contains(err.Error(), "none of the requested sites are configured") {
			return
		}
		resp.Diagnostics.AddError("Error deleting site replication", "Could not delete site replication: "+err.Error())
	}
}

func (r *SiteReplicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func modelToSite(plan SiteReplicationResourceModel) rustfs.SiteReplicationSite {
	return rustfs.SiteReplicationSite{
		Name:          plan.Name.ValueString(),
		Endpoint:      strings.TrimSuffix(plan.Endpoint.ValueString(), "/"),
		AccessKey:     plan.AccessKey.ValueString(),
		SecretKey:     plan.SecretKey.ValueString(),
		SkipTLSVerify: plan.SkipTLSVerify.ValueBool(),
		CACertPEM:     plan.CACertPEM.ValueString(),
	}
}

func modelToPeerEdit(plan SiteReplicationResourceModel) rustfs.SiteReplicationPeerEdit {
	site := modelToSite(plan)
	return rustfs.SiteReplicationPeerEdit{
		Name:          site.Name,
		Endpoint:      site.Endpoint,
		AccessKey:     site.AccessKey,
		SecretKey:     site.SecretKey,
		SkipTLSVerify: site.SkipTLSVerify,
		CACertPEM:     site.CACertPEM,
	}
}

func init() {
	registerResource(NewSiteReplicationResource)
}
