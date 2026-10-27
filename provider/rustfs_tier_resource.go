// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &TierResource{}
	_ resource.ResourceWithImportState = &TierResource{}
)

// RustFS reconciles every tier mutation cluster-wide in the background and
// refuses the next one until it has finished: an edit is answered with
// TierEditFailed ("Remote tier mutation recovery must finish before applying a
// new tier configuration update") and an add or remove with
// XMinioAdminTierBackendInUse ("Specified remote tier is already in use"). Both
// are transient - the same request succeeds a moment later - so the resource
// retries them for a bounded time instead of failing a whole Terraform run.
const (
	tierMutationAttempts = 12
	tierMutationInterval = 500 * time.Millisecond
)

// isTransientTierMutationError reports whether the server refused a tier
// mutation only because a previous one has not finished reconciling.
func isTransientTierMutationError(err error) bool {
	if err == nil {
		return false
	}
	if rustfs.IsAPIErrorCode(err, "TierEditFailed", "XMinioAdminTierBackendInUse") {
		return true
	}
	// Older releases answer without a structured error code.
	message := err.Error()
	return strings.Contains(message, "Remote tier mutation recovery must finish") ||
		strings.Contains(message, "Specified remote tier is already in use")
}

// tierMutation retries one tier registry mutation while the server reports that
// a previous mutation has not finished reconciling.
func tierMutation(mutate func() error) error {
	var err error
	for attempt := range tierMutationAttempts {
		if err = mutate(); err == nil || !isTransientTierMutationError(err) {
			return err
		}
		if attempt < tierMutationAttempts-1 {
			time.Sleep(tierMutationInterval)
		}
	}
	return err
}

type TierResource struct {
	client *AllClient
}

type tierResourceModel struct {
	Name       types.String `tfsdk:"name"`
	TierType   types.String `tfsdk:"tier_type"`
	ConfigJson types.String `tfsdk:"config_json"`
}

func NewTierResource() resource.Resource {
	return &TierResource{}
}

func (r *TierResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tier"
}

func (r *TierResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage RustFS storage tiers",
		MarkdownDescription: "Manage RustFS storage tiers for data transition to external backends",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Tier name (must be uppercase). Changing this forces recreation.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"tier_type": schema.StringAttribute{
				Required:      true,
				Description:   "Tier type: s3, minio, azure, gcs, aliyun, tencent, huaweicloud, r2, or rustfs.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"config_json": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Backend-specific configuration as a JSON string. See RustFS tier documentation for the expected format per backend type.",
			},
		},
	}
}

func (r *TierResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TierResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan tierResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !json.Valid([]byte(plan.ConfigJson.ValueString())) {
		resp.Diagnostics.AddError("Invalid JSON", "config_json must be valid JSON")
		return
	}

	if err := tierMutation(func() error {
		return r.client.RustClient.AddTier(ctx, json.RawMessage(plan.ConfigJson.ValueString()))
	}); err != nil {
		resp.Diagnostics.AddError("Error adding tier", "Could not add tier: "+err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TierResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state tierResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()

	// GET tier/<name> is the server's own verdict on this exact tier: 200 when
	// it holds the tier, 500 TierNotFound when it does not (probed on 1.0.1 and
	// latest). It answers for the one name in state even when the listing is
	// incomplete, so it always decides whether the tier exists.
	exists, err := r.client.RustClient.TierExists(ctx, name)
	if err != nil {
		// A server that does not implement the route cannot answer the
		// question, so keep the last known state instead of guessing; the
		// tier's destroy path has the same rule. An import still needs the
		// type from the listing, so fill it when the state lacks it.
		if rustfs.IsUnsupportedFeature(err) || isUnsupportedTierRoute(err) {
			if !r.fillTierType(ctx, resp, &state) {
				return
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
		resp.Diagnostics.AddError("Error reading tier", "Could not read tier: "+err.Error())
		return
	}

	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	if !r.fillTierType(ctx, resp, &state) {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// fillTierType fills tier_type from the server's tier listing when the state
// does not have it. ImportStatePassthroughID seeds only name, and tier_type is
// Required with RequiresReplace, so leaving it empty would make Terraform plan
// to delete and recreate a tier the server already holds (#116). The listing
// redacts credentials and is therefore used for the type alone.
//
// A missing type is reported as an error rather than left empty: planning a
// destructive replacement is worse than failing the import with a reason. A
// state that already carries the type costs no extra request.
func (r *TierResource) fillTierType(ctx context.Context, resp *resource.ReadResponse, state *tierResourceModel) bool {
	if !state.TierType.IsNull() {
		return true
	}

	tiers, err := r.client.RustClient.ListTiers(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading tier type", "Could not list tiers: "+err.Error())
		return false
	}

	name := state.Name.ValueString()
	for _, tier := range tiers {
		if tier.Name != name {
			continue
		}
		if tier.TierType == "" {
			break
		}
		state.TierType = types.StringValue(tier.TierType)
		return true
	}

	resp.Diagnostics.AddError(
		"Error reading tier type",
		fmt.Sprintf("The server did not report a type for tier %q.", name),
	)
	return false
}

// isUnsupportedTierRoute reports whether err only says the server does not
// implement the per-tier verify route, which is not evidence that the tier is
// gone. RustFS answers 501 NotImplemented (or 405) for a route it registered
// but has not implemented.
func isUnsupportedTierRoute(err error) bool {
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode == http.StatusNotImplemented || apiErr.StatusCode == http.StatusMethodNotAllowed {
		return true
	}
	return apiErr.Code == "NotImplemented" || apiErr.Code == "MethodNotAllowed"
}

func (r *TierResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan tierResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !json.Valid([]byte(plan.ConfigJson.ValueString())) {
		resp.Diagnostics.AddError("Invalid JSON", "config_json must be valid JSON")
		return
	}

	if err := tierMutation(func() error {
		return r.client.RustClient.EditTier(ctx, plan.Name.ValueString(), json.RawMessage(plan.ConfigJson.ValueString()))
	}); err != nil {
		resp.Diagnostics.AddError("Error editing tier", "Could not edit tier: "+err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TierResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data tierResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := tierMutation(func() error {
		return r.client.RustClient.RemoveTier(ctx, data.Name.ValueString())
	}); err != nil {
		resp.Diagnostics.AddError("Error removing tier", "Could not remove tier: "+err.Error())
		return
	}
}

func (r *TierResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func init() {
	registerResource(NewTierResource)
}
