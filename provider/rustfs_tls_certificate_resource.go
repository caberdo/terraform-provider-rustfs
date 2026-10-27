// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &TLSCertificateResource{}

func NewTLSCertificateResource() resource.Resource {
	return &TLSCertificateResource{}
}

// TLSCertificateResource publishes the leaf certificate the RustFS default
// listener serves.
type TLSCertificateResource struct {
	client *AllClient
}

type TLSCertificateResourceModel struct {
	ID                             types.String `tfsdk:"id"`
	CertificatePEM                 types.String `tfsdk:"certificate_pem"`
	KeyPEM                         types.String `tfsdk:"key_pem"`
	SchemaVersion                  types.String `tfsdk:"schema_version"`
	ResourceID                     types.String `tfsdk:"resource_id"`
	TargetRevision                 types.String `tfsdk:"target_revision"`
	CertificateFingerprint         types.String `tfsdk:"certificate_fingerprint"`
	NotAfterUnix                   types.Int64  `tfsdk:"not_after_unix"`
	LastIssuanceID                 types.String `tfsdk:"last_issuance_id"`
	ObservedCertificateFingerprint types.String `tfsdk:"observed_certificate_fingerprint"`
}

func (r *TLSCertificateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tls_certificate"
}

func (r *TLSCertificateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Publish the TLS certificate served by RustFS",
		MarkdownDescription: "Publish the leaf certificate the RustFS default listener serves.\n\n" +
			"Publishing is a whole-file replacement, not a patch, so changing either PEM replaces the " +
			"resource and publishes the new pair. RustFS accepts the request only on its native TLS " +
			"listener (`ssl = true`) and only when started with `RUSTFS_TLS_RELOAD_ENABLE=true` and " +
			"`RUSTFS_TLS_PUBLISH_DIR` pointing at a private directory outside `RUSTFS_TLS_PATH`.\n\n" +
			"RustFS has no route that removes a published certificate. Destroying this resource " +
			"therefore only removes it from Terraform state (with a warning); the server keeps serving " +
			"the last published certificate until another publication replaces it or the server is " +
			"restarted with different TLS material.\n\n" +
			"Import is not supported: the PEM pair is write-only and the server never returns it, so an " +
			"imported resource could not describe the certificate it manages.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Identifier of the publication target, as reported by the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"certificate_pem": schema.StringAttribute{
				Required:    true,
				Description: "PEM-encoded leaf certificate chain to publish. The first certificate is the leaf; its subject alternative names must include every name the currently served certificate covers.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"key_pem": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "PEM-encoded private key for the leaf certificate. RustFS does not return it, so it is stored in Terraform state.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"schema_version": schema.StringAttribute{
				Computed:    true,
				Description: "Publication schema version the server installed (rustfs.tls-publish/v1alpha1).",
			},
			"resource_id": schema.StringAttribute{
				Computed:    true,
				Description: "Publication target the server reports (server-default).",
			},
			"target_revision": schema.StringAttribute{
				Computed:    true,
				Description: "Revision of the installed publication. A bootstrap certificate has a bootstrap-<fingerprint> revision until the first publication.",
			},
			"certificate_fingerprint": schema.StringAttribute{
				Computed:    true,
				Description: "Lowercase SHA-256 fingerprint of the installed leaf certificate (DER).",
			},
			"not_after_unix": schema.Int64Attribute{
				Computed:    true,
				Description: "Expiry of the installed leaf certificate as a Unix timestamp.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"last_issuance_id": schema.StringAttribute{
				Computed:    true,
				Description: "Issuance ID of the publication the server installed. Null when the served certificate is the server's bootstrap material.",
			},
			"observed_certificate_fingerprint": schema.StringAttribute{
				Computed:    true,
				Description: "Fingerprint of the certificate the listener currently serves. Null until the listener has reloaded the publication.",
			},
		},
	}
}

func (r *TLSCertificateResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TLSCertificateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TLSCertificateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	receipt, err := r.client.RustClient.PublishTLSCertificate(
		ctx, plan.CertificatePEM.ValueString(),
		plan.KeyPEM.ValueString(),
	)
	if err != nil {
		addTLSCertificateDiagnostic(&resp.Diagnostics, "Error publishing TLS certificate",
			"Could not publish the TLS certificate: ", err)
		return
	}

	applyTLSCertificateReceipt(&plan, receipt)
	tflog.Trace(ctx, "published a TLS certificate", map[string]any{
		"target_revision": receipt.TargetRevision,
	})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TLSCertificateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TLSCertificateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	certificateState, err := r.client.RustClient.GetTLSCertificateState(ctx)
	if err != nil {
		addTLSCertificateDiagnostic(&resp.Diagnostics, "Error reading TLS certificate state",
			"Could not read the TLS certificate state: ", err)
		return
	}

	// last_issuance_id is null only while the server serves its startup
	// material. That certificate was not published by this resource, so there
	// is nothing left to manage until Terraform publishes again.
	if certificateState.LastIssuanceID == nil {
		tflog.Trace(ctx, "no published TLS certificate, removing from state")
		resp.State.RemoveResource(ctx)
		return
	}

	// The configured PEM pair is write-only, so a server that serves a
	// different leaf means the publication was replaced outside Terraform.
	// Dropping the resource makes the next plan publish the configured pair
	// again instead of claiming a certificate it does not own.
	if expected, ok := certificateFingerprintFromPEM(state.CertificatePEM.ValueString()); ok &&
		expected != certificateState.CertificateFingerprint {
		tflog.Trace(ctx, "the served TLS certificate was published outside Terraform, removing from state",
			map[string]any{
				"configured_fingerprint": expected,
				"served_fingerprint":     certificateState.CertificateFingerprint,
			})
		resp.State.RemoveResource(ctx)
		return
	}

	applyTLSCertificateState(&state, certificateState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TLSCertificateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TLSCertificateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Every configurable attribute is RequiresReplace, so Terraform plans a
	// replacement instead of calling this method. Writing the plan back keeps
	// the state consistent if the framework invokes it anyway.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TLSCertificateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TLSCertificateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// RustFS exposes no route that removes a published certificate. Calling a
	// route that does not exist cannot make destroy work, so destroy only drops
	// the resource from state and tells the operator what the server keeps.
	fingerprint := state.CertificateFingerprint.ValueString()
	if fingerprint == "" {
		fingerprint = "the last published certificate"
	} else {
		fingerprint = "certificate " + fingerprint
	}
	resp.Diagnostics.AddWarning(
		"TLS certificate left published",
		"Removed the TLS certificate from Terraform state, but RustFS has no route that unpublishes a "+
			"certificate. The server keeps serving "+fingerprint+" until another publication replaces it "+
			"or the server is restarted with different TLS material.",
	)
	tflog.Trace(ctx, "removed a TLS certificate resource from state without unpublishing it")
}

// applyTLSCertificateReceipt writes a successful publication response into the
// model. The receipt carries the issuance it just created instead of a last
// issuance ID.
func applyTLSCertificateReceipt(model *TLSCertificateResourceModel, receipt rustfs.TLSCertificatePublishReceipt) {
	model.ID = types.StringValue(receipt.ResourceID)
	model.SchemaVersion = types.StringValue(receipt.SchemaVersion)
	model.ResourceID = types.StringValue(receipt.ResourceID)
	model.TargetRevision = types.StringValue(receipt.TargetRevision)
	model.CertificateFingerprint = types.StringValue(receipt.CertificateFingerprint)
	model.NotAfterUnix = types.Int64Value(receipt.NotAfterUnix)
	model.LastIssuanceID = types.StringValue(receipt.IssuanceID)
	model.ObservedCertificateFingerprint = stringFromPtr(receipt.ObservedCertificateFingerprint)
}

// applyTLSCertificateState writes a GET certificate-state response into the
// model, preserving the write-only PEM attributes.
func applyTLSCertificateState(model *TLSCertificateResourceModel, state rustfs.TLSCertificateState) {
	model.ID = types.StringValue(state.ResourceID)
	model.SchemaVersion = types.StringValue(state.SchemaVersion)
	model.ResourceID = types.StringValue(state.ResourceID)
	model.TargetRevision = types.StringValue(state.TargetRevision)
	model.CertificateFingerprint = types.StringValue(state.CertificateFingerprint)
	model.NotAfterUnix = types.Int64Value(state.NotAfterUnix)
	model.LastIssuanceID = stringFromPtr(state.LastIssuanceID)
	model.ObservedCertificateFingerprint = stringFromPtr(state.ObservedCertificateFingerprint)
}

// certificateFingerprintFromPEM returns the lowercase SHA-256 fingerprint of
// the first certificate in certificatePEM, the same value RustFS reports for
// the leaf it serves. It returns false when the text is not a parseable
// certificate, so Read never drops state over a config the server has not yet
// rejected.
func certificateFingerprintFromPEM(certificatePEM string) (string, bool) {
	rest := []byte(certificatePEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return "", false
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return "", false
		}
		sum := sha256.Sum256(certificate.Raw)
		return hex.EncodeToString(sum[:]), true
	}
}

// addTLSCertificateDiagnostic reports a TLS certificate API failure. The three
// conditions an operator can fix - a plain-HTTP endpoint, a server without
// publication enabled, and a lost publication race - get their own summary;
// every other error keeps the caller's context.
func addTLSCertificateDiagnostic(diags *diag.Diagnostics, summary, detailPrefix string, err error) {
	switch {
	case errors.Is(err, rustfs.ErrTLSPublishTooLarge):
		diags.AddError(
			"TLS certificate request exceeds the RustFS body limit",
			"RustFS rejects a publication request whose body is larger than 128 KiB. "+
				"Reduce certificate_pem or key_pem (a full chain counts towards the limit) and try again.",
		)
	case rustfs.IsUnsupportedFeature(err):
		diags.AddError(summary, err.Error())
	case tlsCertificatePlainHTTP(err):
		diags.AddError(
			"TLS certificate management needs the native TLS listener",
			"This endpoint is `ssl = false`: RustFS only accepts certificate management on its native "+
				"TLS listener. Point the provider at the HTTPS admin endpoint of a listener started with "+
				"`RUSTFS_TLS_PATH`, and set `ssl = true`. Server response: "+err.Error(),
		)
	case tlsCertificatePublicationDisabled(err):
		diags.AddError(
			"TLS certificate publication is not enabled",
			"RustFS refused the publication because it is disabled or not configured safely. Start the "+
				"server with `RUSTFS_TLS_RELOAD_ENABLE=true` and `RUSTFS_TLS_PUBLISH_DIR` set to a private "+
				"directory outside `RUSTFS_TLS_PATH`. Server response: "+err.Error(),
		)
	case tlsCertificatePublicationConflict(err):
		diags.AddError(
			"TLS certificate publication conflict",
			"The publication revision changed while the certificate was being published, so the server "+
				"rejected the compare-and-swap. Re-run the apply to publish against the current revision. "+
				"Server response: "+err.Error(),
		)
	default:
		diags.AddError(summary, detailPrefix+err.Error())
	}
}

// tlsCertificatePlainHTTP reports RustFS's refusal to manage certificates on a
// listener that is not its native TLS transport.
func tlsCertificatePlainHTTP(err error) bool {
	return rustfs.IsAPIErrorCode(err, "AccessDenied") &&
		strings.Contains(err.Error(), "certificate management requires TLS on the RustFS listener")
}

// tlsCertificatePublicationDisabled reports RustFS's answer when publication
// is switched off or the publication directory fails its safety checks.
func tlsCertificatePublicationDisabled(err error) bool {
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	return strings.Contains(apiErr.Error(), "certificate publication is disabled or not configured safely")
}

// tlsCertificatePublicationConflict reports a lost compare-and-swap on the
// publication revision.
func tlsCertificatePublicationConflict(err error) bool {
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		return false
	}
	return strings.Contains(apiErr.Error(), "certificate publication revision conflict")
}

func init() {
	registerResource(NewTLSCertificateResource)
}
