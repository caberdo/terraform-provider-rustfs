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
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                   = &kmsKeyResource{}
	_ resource.ResourceWithImportState    = &kmsKeyResource{}
	_ resource.ResourceWithValidateConfig = &kmsKeyResource{}
)

// NewKmsKeyResource is a helper function to simplify the provider implementation.
func NewKmsKeyResource() resource.Resource {
	return &kmsKeyResource{}
}

// kmsKeyResource is the resource implementation.
type kmsKeyResource struct {
	client *AllClient
}

type kmsKeyResourceModel struct {
	Name               types.String `tfsdk:"name"`
	KeyID              types.String `tfsdk:"key_id"`
	CreatedAt          types.String `tfsdk:"created_at"`
	Enabled            types.Bool   `tfsdk:"enabled"`
	SkipDestroy        types.Bool   `tfsdk:"skip_destroy"`
	Description        types.String `tfsdk:"description"`
	Tags               types.Map    `tfsdk:"tags"`
	RotateTrigger      types.String `tfsdk:"rotate_trigger"`
	DeletionWindowDays types.Int64  `tfsdk:"deletion_window_days"`
}

// Metadata returns the resource type name.
func (r *kmsKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kms_key"
}

// Schema defines the schema for the resource.
func (r *kmsKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage RustFS KMS master keys",
		MarkdownDescription: "Manage RustFS KMS master keys. Deleting a KMS master key is **critical and irreversible**: it schedules the destruction of the key material and makes every object encrypted under the key permanently unreadable. Guard the key with `skip_destroy = true`.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Name of the KMS master key. Changing this forces recreation.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"key_id": schema.StringAttribute{
				Computed:    true,
				Description: "Server-generated id of the KMS master key. On the Local dev backend this equals the key name.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the key was created.",
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the KMS master key is enabled. Defaults to `true`.",
			},
			"skip_destroy": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "When `true`, destroy only removes Terraform state and leaves the irreversible server-side key deletion unexecuted. Defaults to `false`.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Description stored on the key. Set it to an empty string to clear it; omitting it preserves the server value.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"tags": schema.MapAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Description: "Tags stored on the key. The reserved `name` tag is managed by RustFS and is never read back into state. Removing a tag untags it on the server.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.UseStateForUnknown(),
				},
			},
			"rotate_trigger": schema.StringAttribute{
				Optional: true,
				Description: "When this value changes to a new non-null value, the key is rotated. Removing it does not " +
					"rotate. The server returns no rotation version or timestamp, so no computed rotation attribute exists.",
			},
			"deletion_window_days": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(30),
				Validators:  []validator.Int64{int64validator.Between(7, 30)},
				Description: "Days between destroy and permanent key-material destruction (7-30). Consumed at destroy; changing it does not call the server.",
			},
		},
	}
}

func (r *kmsKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig rejects the reserved name tag, which RustFS owns and the
// provider therefore cannot manage through the tags map.
func (r *kmsKeyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config kmsKeyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Tags.IsNull() || config.Tags.IsUnknown() {
		return
	}
	var tags map[string]string
	resp.Diagnostics.Append(config.Tags.ElementsAs(ctx, &tags, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, reserved := tags["name"]; reserved {
		resp.Diagnostics.AddAttributeError(
			path.Root("tags"),
			"Reserved KMS key tag",
			"the `name` tag is reserved by RustFS and cannot be managed through tags; it is derived from the key name.",
		)
	}
}

// kmsKeyTagsFromMap decodes the tag map, treating null as no tags.
func kmsKeyTagsFromMap(ctx context.Context, m types.Map) (map[string]string, diag.Diagnostics) {
	if m.IsNull() || m.IsUnknown() {
		return map[string]string{}, nil
	}
	var tags map[string]string
	diags := m.ElementsAs(ctx, &tags, false)
	if diags.HasError() {
		return nil, diags
	}
	if tags == nil {
		tags = map[string]string{}
	}
	return tags, nil
}

// kmsKeyTagsValue renders server tags without the reserved name tag.
func kmsKeyTagsValue(tags map[string]string) types.Map {
	values := make(map[string]attr.Value, len(tags))
	for key, value := range tags {
		if key == "name" {
			continue
		}
		values[key] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, values)
}

// applyKmsKey copies the server-reported key identity and metadata into model.
// Configuration-only attributes are left untouched.
func applyKmsKey(ctx context.Context, model *kmsKeyResourceModel, key rustfs.KmsKey) {
	model.KeyID = types.StringValue(key.KeyID)
	model.CreatedAt = types.StringValue(key.CreationDate)
	model.Enabled = types.BoolValue(key.KeyState == "Enabled")
	model.Description = stringFromPtr(key.Description)
	model.Tags = kmsKeyTagsValue(key.Tags)
}

// Create creates the KMS key and sets the initial Terraform state.
func (r *kmsKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kmsKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	key, err := r.client.RustClient.CreateKmsKey(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating KMS key",
			"Could not create KMS key, unexpected error: "+err.Error(),
		)
		return
	}
	plan.KeyID = types.StringValue(key.KeyID)
	plan.CreatedAt = types.StringValue(key.CreationDate)
	// An attribute omitted from configuration arrives as unknown, not null, so
	// both mean "not configured" here: adopt what the server created.
	if plan.Description.IsNull() || plan.Description.IsUnknown() {
		plan.Description = stringFromPtr(key.Description)
	}
	if plan.Tags.IsNull() || plan.Tags.IsUnknown() {
		plan.Tags = kmsKeyTagsValue(key.Tags)
	}

	description := plan.Description.ValueString()
	if !plan.Description.IsNull() && description != "" {
		if _, err := r.client.RustClient.UpdateKmsKeyDescription(ctx, key.KeyID, description); err != nil {
			resp.Diagnostics.AddError(
				"Error updating KMS key description",
				"Could not update KMS key description, unexpected error: "+err.Error(),
			)
			return
		}
	}

	tags, diags := kmsKeyTagsFromMap(ctx, plan.Tags)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(tags) > 0 {
		if _, err := r.client.RustClient.TagKmsKey(ctx, key.KeyID, tags); err != nil {
			resp.Diagnostics.AddError(
				"Error updating KMS key tags",
				"Could not update KMS key tags, unexpected error: "+err.Error(),
			)
			return
		}
	}

	if !plan.Enabled.ValueBool() {
		if err := r.client.RustClient.DisableKmsKey(ctx, key.KeyID); err != nil {
			resp.Diagnostics.AddError(
				"Error disabling KMS key",
				"Could not disable KMS key, unexpected error: "+err.Error(),
			)
			return
		}
	}
	tflog.Trace(ctx, "created a KMS key")
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the Terraform state with the latest data.
func (r *kmsKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kmsKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyID := state.KeyID.ValueString()
	if keyID == "" {
		keyID = r.resolveKeyID(ctx, state.Name.ValueString(), resp)
		if resp.Diagnostics.HasError() {
			return
		}
		if keyID == "" {
			resp.State.RemoveResource(ctx)
			return
		}
		state.KeyID = types.StringValue(keyID)
	}

	key, err := r.client.RustClient.DescribeKmsKey(ctx, keyID)
	if err != nil {
		if isKmsKeyNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading KMS key",
			"Could not read KMS key, unexpected error: "+err.Error(),
		)
		return
	}
	applyKmsKey(ctx, &state, key)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// isKmsKeyNotFound reports whether err means the KMS master key is missing.
//
// Probed against a RustFS with the local KMS backend: describing a key the
// server does not hold answers 404 with the body
// `{"success":false,"message":"Failed to describe key: Key not found: <id>"}`,
// and DescribeKmsKey turns a 200 body with a null key_metadata into a plain
// error carrying that same message. Both shapes are matched on the endpoint's
// own "Key not found" wording — not on any "not found" substring, so an
// unrelated failure (for example 503 "kms service is not running") is still
// reported instead of being mistaken for an idempotent delete.
func isKmsKeyNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *rustfs.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "key not found")
}

// resolveKeyID maps a key name to its server-side key id by listing the keys
// when the state does not yet carry one (import path). On the Local dev
// backend the key id equals the name; on production backends the key created
// through this provider carries a `name` tag that the listing exposes.
func (r *kmsKeyResource) resolveKeyID(ctx context.Context, name string, resp *resource.ReadResponse) string {
	keys, err := r.client.RustClient.ListKmsKeys(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing KMS keys",
			"Could not resolve key id from name, unexpected error: "+err.Error(),
		)
		return ""
	}
	for _, key := range keys {
		if key.KeyID == name || key.Tags["name"] == name {
			return key.KeyID
		}
	}
	tflog.Warn(ctx, "no KMS key matched name, removing from state", map[string]any{"name": name})
	return ""
}

// Update enables or disables the key and updates the Terraform state on success.
func (r *kmsKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state kmsKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyID := plan.KeyID.ValueString()
	if keyID == "" {
		keyID = plan.Name.ValueString()
	}

	if !plan.Description.Equal(state.Description) {
		if _, err := r.client.RustClient.UpdateKmsKeyDescription(ctx, keyID, plan.Description.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Error updating KMS key description",
				"Could not update KMS key description, unexpected error: "+err.Error(),
			)
			return
		}
	}

	desiredTags, diags := kmsKeyTagsFromMap(ctx, plan.Tags)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	currentTags, diags := kmsKeyTagsFromMap(ctx, state.Tags)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	additions := map[string]string{}
	for key, value := range desiredTags {
		if currentTags[key] != value {
			additions[key] = value
		}
	}
	removals := make([]string, 0)
	for key := range currentTags {
		if _, keep := desiredTags[key]; !keep {
			removals = append(removals, key)
		}
	}
	if len(additions) > 0 {
		if _, err := r.client.RustClient.TagKmsKey(ctx, keyID, additions); err != nil {
			resp.Diagnostics.AddError(
				"Error updating KMS key tags",
				"Could not update KMS key tags, unexpected error: "+err.Error(),
			)
			return
		}
	}
	if len(removals) > 0 {
		if _, err := r.client.RustClient.UntagKmsKey(ctx, keyID, removals); err != nil {
			resp.Diagnostics.AddError(
				"Error updating KMS key tags",
				"Could not remove KMS key tags, unexpected error: "+err.Error(),
			)
			return
		}
	}
	plan.Tags = kmsKeyTagsValue(desiredTags)

	if !plan.Enabled.Equal(state.Enabled) {
		var err error
		if plan.Enabled.ValueBool() {
			err = r.client.RustClient.EnableKmsKey(ctx, keyID)
		} else {
			err = r.client.RustClient.DisableKmsKey(ctx, keyID)
		}
		if err != nil {
			resp.Diagnostics.AddError(
				"Error updating KMS key",
				"Could not update KMS key, unexpected error: "+err.Error(),
			)
			return
		}
	}

	// Rotation goes last: metadata failures before it leave the trigger unsaved,
	// so a retry does not rotate twice.
	if !plan.RotateTrigger.IsNull() && !plan.RotateTrigger.Equal(state.RotateTrigger) {
		if _, err := r.client.RustClient.RotateKmsKey(ctx, keyID); err != nil {
			resp.Diagnostics.AddError(
				"Error rotating KMS key",
				"Could not rotate KMS key, unexpected error: "+err.Error(),
			)
			return
		}
	}

	// Computed-only attributes are carried over from the prior state: these
	// updates do not change the key id or creation date.
	plan.KeyID = state.KeyID
	plan.CreatedAt = state.CreatedAt
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete irreversibly deletes the KMS key unless skip_destroy is set.
func (r *kmsKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state kmsKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.SkipDestroy.ValueBool() {
		tflog.Warn(ctx, "skip_destroy=true: leaving KMS key in place, removing only Terraform state")
		return
	}

	keyID := state.KeyID.ValueString()
	if keyID == "" {
		keyID = state.Name.ValueString()
	}
	window := int64(30)
	if !state.DeletionWindowDays.IsNull() && !state.DeletionWindowDays.IsUnknown() && state.DeletionWindowDays.ValueInt64() > 0 {
		window = state.DeletionWindowDays.ValueInt64()
	}
	if err := r.client.RustClient.DeleteKmsKeyWithWindow(ctx, keyID, &window); err != nil {
		// A key that is already gone (deleted or scheduled for deletion) has
		// nothing left to destroy, so the destroy is a no-op rather than a
		// failure.
		if isKmsKeyNotFound(err) {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting KMS key",
			"Could not delete KMS key, unexpected error: "+err.Error(),
		)
	}
}

func (r *kmsKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func init() {
	registerResource(NewKmsKeyResource)
}
