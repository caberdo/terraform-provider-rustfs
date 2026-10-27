// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &TLSCertificateStateDataSource{}

func NewTLSCertificateStateDataSource() datasource.DataSource {
	return &TLSCertificateStateDataSource{}
}

// TLSCertificateStateDataSource reads the TLS certificate the RustFS default
// listener serves.
type TLSCertificateStateDataSource struct {
	client *AllClient
}

type TLSCertificateStateDataSourceModel struct {
	SchemaVersion                  types.String `tfsdk:"schema_version"`
	ResourceID                     types.String `tfsdk:"resource_id"`
	TargetRevision                 types.String `tfsdk:"target_revision"`
	CertificateFingerprint         types.String `tfsdk:"certificate_fingerprint"`
	NotAfterUnix                   types.Int64  `tfsdk:"not_after_unix"`
	LastIssuanceID                 types.String `tfsdk:"last_issuance_id"`
	ObservedCertificateFingerprint types.String `tfsdk:"observed_certificate_fingerprint"`
}

func (d *TLSCertificateStateDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tls_certificate_state"
}

func (d *TLSCertificateStateDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Read the TLS certificate served by RustFS",
		MarkdownDescription: "Read the TLS certificate the RustFS default listener serves, including the " +
			"publication revision and fingerprints.\n\n" +
			"The route is only available on RustFS 1.0.2 or later, and only on the native TLS listener " +
			"(`ssl = true`).",
		Attributes: map[string]schema.Attribute{
			"schema_version": schema.StringAttribute{
				Computed:    true,
				Description: "Publication schema version the server reports (rustfs.tls-publish/v1alpha1).",
			},
			"resource_id": schema.StringAttribute{
				Computed:    true,
				Description: "Publication target the server reports (server-default).",
			},
			"target_revision": schema.StringAttribute{
				Computed:    true,
				Description: "Revision of the current publication. A bootstrap certificate has a bootstrap-<fingerprint> revision until the first publication.",
			},
			"certificate_fingerprint": schema.StringAttribute{
				Computed:    true,
				Description: "Lowercase SHA-256 fingerprint of the leaf certificate in the current publication (DER).",
			},
			"not_after_unix": schema.Int64Attribute{
				Computed:    true,
				Description: "Expiry of the leaf certificate as a Unix timestamp.",
			},
			"last_issuance_id": schema.StringAttribute{
				Computed:    true,
				Description: "Issuance ID of the current publication. Null while the server serves its bootstrap certificate.",
			},
			"observed_certificate_fingerprint": schema.StringAttribute{
				Computed:    true,
				Description: "Fingerprint of the certificate the listener currently serves. Null until the listener has reloaded a publication.",
			},
		},
	}
}

func (d *TLSCertificateStateDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TLSCertificateStateDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config TLSCertificateStateDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	certificateState, err := d.client.RustClient.GetTLSCertificateState(ctx)
	if err != nil {
		addTLSCertificateDiagnostic(&resp.Diagnostics, "Error reading TLS certificate state",
			"Could not read the TLS certificate state: ", err)
		return
	}

	config.SchemaVersion = types.StringValue(certificateState.SchemaVersion)
	config.ResourceID = types.StringValue(certificateState.ResourceID)
	config.TargetRevision = types.StringValue(certificateState.TargetRevision)
	config.CertificateFingerprint = types.StringValue(certificateState.CertificateFingerprint)
	config.NotAfterUnix = types.Int64Value(certificateState.NotAfterUnix)
	config.LastIssuanceID = stringFromPtr(certificateState.LastIssuanceID)
	config.ObservedCertificateFingerprint = stringFromPtr(certificateState.ObservedCertificateFingerprint)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func init() {
	registerDataSource(NewTLSCertificateStateDataSource)
}
