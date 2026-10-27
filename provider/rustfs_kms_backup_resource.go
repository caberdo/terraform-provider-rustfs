// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &kmsBackupResource{}

// NewKmsBackupResource creates one RustFS KMS backup bundle.
func NewKmsBackupResource() resource.Resource {
	return &kmsBackupResource{}
}

type kmsBackupResource struct {
	client *AllClient
}

type kmsBackupResourceModel struct {
	ID                      types.String `tfsdk:"id"`
	BackupID                types.String `tfsdk:"backup_id"`
	SnapshotGeneration      types.Int64  `tfsdk:"snapshot_generation"`
	DeploymentIdentity      types.String `tfsdk:"deployment_identity"`
	ArtifactCount           types.Int64  `tfsdk:"artifact_count"`
	ManifestDigest          types.String `tfsdk:"manifest_digest"`
	BackupKEKID             types.String `tfsdk:"backup_kek_id"`
	BackupKEKVersion        types.Int64  `tfsdk:"backup_kek_version"`
	SanitizedConfigIncluded types.Bool   `tfsdk:"sanitized_config_included"`
	ExportSupported         types.Bool   `tfsdk:"export_supported"`
	UnavailableReason       types.String `tfsdk:"unavailable_reason"`
}

func (r *kmsBackupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kms_backup"
}

func (r *kmsBackupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Create one RustFS KMS backup bundle",
		MarkdownDescription: "Create one RustFS KMS backup bundle under the server's configured backup root.\n\n" +
			"This records the bundle's metadata. The server exposes no route to read, verify or " +
			"delete a specific bundle, so refresh only reports whether the backup subsystem is still " +
			"available, and destroy only forgets the record; the bundle stays on the server.\n\n" +
			"Create first asks `GET /rustfs/admin/v3/kms/backup` whether the server can export. When " +
			"it reports `export_supported = false` the apply fails with the server's reason and names " +
			"`RUSTFS_KMS_BACKUP_DIR`, `RUSTFS_KMS_BACKUP_KEK` and `RUSTFS_KMS_BACKUP_KEK_ID`, and no " +
			"bundle is written.\n\n" +
			"The create route returns identifiers and metadata only, never key material, so no secret " +
			"enters state. Supplying an explicit `backup_id` is required: the server refuses to mix " +
			"bundles in a non-empty destination, so a deterministic name is what makes a retry " +
			"fail loudly instead of leaving a second orphan bundle.\n\n" +
			"Restore is not managed by this provider. It destroys and replaces key material, requires " +
			"the backup id and conflict policy as separate confirmations, and needs the KMS master key " +
			"out of band.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Terraform resource ID; the bundle's backup id.",
			},
			"backup_id": schema.StringAttribute{
				Required: true,
				Description: "Bundle name under the server's backup root. Must be a single path component of " +
					"letters, digits, dots, dashes and underscores, and must not start with a dot. Changing it creates a new bundle.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 128),
					stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`), "must be a single path component of letters, digits, dots, dashes and underscores"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"snapshot_generation": schema.Int64Attribute{
				Computed:    true,
				Description: "Monotonic snapshot generation recorded in the bundle manifest.",
			},
			"deployment_identity": schema.StringAttribute{
				Computed:    true,
				Description: "Deployment identity the bundle is bound to; a restore refuses a foreign bundle.",
			},
			"artifact_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of artifacts sealed into the bundle.",
			},
			"manifest_digest": schema.StringAttribute{
				Computed:    true,
				Description: "Digest of the bundle manifest.",
			},
			"backup_kek_id": schema.StringAttribute{
				Computed:    true,
				Description: "Identifier of the backup KEK that sealed the bundle. Not the key material.",
			},
			"backup_kek_version": schema.Int64Attribute{
				Computed:    true,
				Description: "Version of the backup KEK that sealed the bundle.",
			},
			"sanitized_config_included": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the bundle carries a sanitized KMS configuration artifact.",
			},
			"export_supported": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the server can still export a bundle. A bundle cannot be verified individually.",
			},
			"unavailable_reason": schema.StringAttribute{
				Computed:    true,
				Description: "Why export is unavailable, when it is.",
			},
		},
	}
}

func (r *kmsBackupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *kmsBackupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kmsBackupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, statusErr := r.client.RustClient.GetKmsBackupStatus(ctx)
	if statusErr != nil {
		resp.Diagnostics.AddError(
			"Error creating KMS backup",
			"Could not check whether this server can export a KMS backup bundle: "+statusErr.Error(),
		)
		return
	}
	if !status.ExportSupported {
		detail := "KMS backup export is disabled on this server, so no bundle was written."
		if status.UnavailableReason != nil && *status.UnavailableReason != "" {
			detail += " The server reports: " + *status.UnavailableReason + "."
		}
		detail += " Set RUSTFS_KMS_BACKUP_DIR, RUSTFS_KMS_BACKUP_KEK and RUSTFS_KMS_BACKUP_KEK_ID on " +
			"every node, restart RustFS, and retry."
		resp.Diagnostics.AddError("KMS backup export is disabled", detail)
		return
	}

	bundle, err := r.client.RustClient.CreateKmsBackup(ctx, plan.BackupID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating KMS backup",
			"Could not create KMS backup bundle: "+err.Error(),
		)
		return
	}

	plan.ID = types.StringValue(bundle.BackupID)
	plan.BackupID = types.StringValue(bundle.BackupID)
	plan.SnapshotGeneration = types.Int64Value(int64(bundle.SnapshotGeneration))
	plan.DeploymentIdentity = types.StringValue(bundle.DeploymentIdentity)
	plan.ArtifactCount = types.Int64Value(int64(bundle.ArtifactCount))
	plan.ManifestDigest = types.StringValue(bundle.ManifestDigest)
	plan.BackupKEKID = types.StringValue(bundle.BackupKEKID)
	plan.BackupKEKVersion = types.Int64Value(int64(bundle.BackupKEKVersion))
	plan.SanitizedConfigIncluded = types.BoolValue(bundle.SanitizedConfigIncluded)
	plan.ExportSupported = types.BoolValue(status.ExportSupported)
	plan.UnavailableReason = stringFromPtr(status.UnavailableReason)

	tflog.Trace(ctx, "created a KMS backup bundle", map[string]any{"backup_id": bundle.BackupID})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *kmsBackupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kmsBackupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := r.client.RustClient.GetKmsBackupStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading KMS backup status",
			"Could not read KMS backup status: "+err.Error(),
		)
		return
	}

	// The server cannot describe an individual bundle, so the recorded bundle
	// metadata stays authoritative and only subsystem readiness is refreshed.
	state.ExportSupported = types.BoolValue(status.ExportSupported)
	state.UnavailableReason = stringFromPtr(status.UnavailableReason)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kmsBackupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan kmsBackupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// backup_id is RequiresReplace, so this runs only if the framework calls
	// Update defensively.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *kmsBackupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state kmsBackupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Warn(ctx, "RustFS has no admin route to delete a KMS backup bundle; removing only Terraform state",
		map[string]any{"backup_id": state.BackupID.ValueString()})
}

func init() {
	registerResource(NewKmsBackupResource)
}
