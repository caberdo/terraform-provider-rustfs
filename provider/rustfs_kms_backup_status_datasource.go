// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &KmsBackupStatusDataSource{}

// KmsBackupStatusDataSource reads whether the server can export a KMS bundle.
type KmsBackupStatusDataSource struct {
	client *AllClient
}

type KmsBackupStatusDataSourceModel struct {
	ServiceStatus       types.String `tfsdk:"service_status"`
	Backend             types.String `tfsdk:"backend"`
	Responsibility      types.String `tfsdk:"responsibility"`
	ExportSupported     types.Bool   `tfsdk:"export_supported"`
	DeploymentIdentity  types.String `tfsdk:"deployment_identity"`
	BackupRoot          types.String `tfsdk:"backup_root"`
	BackupKEKConfigured types.Bool   `tfsdk:"backup_kek_configured"`
	BackupKEKID         types.String `tfsdk:"backup_kek_id"`
	BackupKEKVersion    types.Int64  `tfsdk:"backup_kek_version"`
	TargetKeyDir        types.String `tfsdk:"target_key_dir"`
	UnavailableReason   types.String `tfsdk:"unavailable_reason"`
}

func NewKmsBackupStatusDataSource() datasource.DataSource {
	return &KmsBackupStatusDataSource{}
}

func (d *KmsBackupStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kms_backup_status"
}

func (d *KmsBackupStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get RustFS KMS backup readiness",
		MarkdownDescription: "Read whether the RustFS server can export a KMS backup bundle, and which bundle " +
			"responsibility the configured backend can claim.\n\n" +
			"This reports identifiers, paths and readiness only; it never returns key material. A " +
			"`false` value for `export_supported` means backup is disabled until the server's " +
			"`RUSTFS_KMS_BACKUP_DIR`, `RUSTFS_KMS_BACKUP_KEK` and `RUSTFS_KMS_BACKUP_KEK_ID` are set.",
		Attributes: map[string]schema.Attribute{
			"service_status": schema.StringAttribute{
				Computed:    true,
				Description: "KMS service status, for example `healthy` or `unavailable`.",
			},
			"backend": schema.StringAttribute{
				Computed:    true,
				Description: "Configured KMS backend, when the server reports one.",
			},
			"responsibility": schema.StringAttribute{
				Computed:    true,
				Description: "What a bundle of the configured backend can be responsible for, for example `full-material`.",
			},
			"export_supported": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether this server can export a bundle right now.",
			},
			"deployment_identity": schema.StringAttribute{
				Computed:    true,
				Description: "Deployment identity a bundle is bound to.",
			},
			"backup_root": schema.StringAttribute{
				Computed:    true,
				Description: "Directory bundles are written under.",
			},
			"backup_kek_configured": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the independent backup KEK is configured.",
			},
			"backup_kek_id": schema.StringAttribute{
				Computed:    true,
				Description: "Identifier of the backup KEK. Not the key material.",
			},
			"backup_kek_version": schema.Int64Attribute{
				Computed:    true,
				Description: "Version of the backup KEK.",
			},
			"target_key_dir": schema.StringAttribute{
				Computed:    true,
				Description: "Key directory a restore would target.",
			},
			"unavailable_reason": schema.StringAttribute{
				Computed:    true,
				Description: "Why the backup subsystem is not usable, when it is not.",
			},
		},
	}
}

func (d *KmsBackupStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *KmsBackupStatusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config KmsBackupStatusDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := d.client.RustClient.GetKmsBackupStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading KMS backup status",
			"Could not read KMS backup status: "+err.Error(),
		)
		return
	}

	config = kmsBackupStatusModelFromStatus(status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func kmsBackupStatusModelFromStatus(status rustfs.KmsBackupStatus) KmsBackupStatusDataSourceModel {
	return KmsBackupStatusDataSourceModel{
		ServiceStatus:       types.StringValue(status.ServiceStatus),
		Backend:             stringFromPtr(status.Backend),
		Responsibility:      stringFromPtr(status.Responsibility),
		ExportSupported:     types.BoolValue(status.ExportSupported),
		DeploymentIdentity:  stringFromPtr(status.DeploymentIdentity),
		BackupRoot:          stringFromPtr(status.BackupRoot),
		BackupKEKConfigured: types.BoolValue(status.BackupKEKConfigured),
		BackupKEKID:         stringFromPtr(status.BackupKEKID),
		BackupKEKVersion:    types.Int64PointerValue(status.BackupKEKVersion),
		TargetKeyDir:        stringFromPtr(status.TargetKeyDir),
		UnavailableReason:   stringFromPtr(status.UnavailableReason),
	}
}

func init() {
	registerDataSource(NewKmsBackupStatusDataSource)
}
