// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// FeatureTLSCertificate is the TLS certificate management capability:
// publishing the served leaf and reading its state. Both routes are declared
// in the same feature because a server ships them together.
const FeatureTLSCertificate Feature = "TLS certificate management"

const (
	// TLSPublishSchemaVersion is the only publication schema RustFS 1.0.2 and
	// 1.0.3 accept. It is a fixed protocol field, not user configuration.
	TLSPublishSchemaVersion = "rustfs.tls-publish/v1alpha1"
	// TLSPublishResourceID is the only publication target RustFS implements:
	// the leaf certificate of the default listener.
	TLSPublishResourceID = "server-default"
	// TLSPublishBodyLimit is the server's body limit for
	// POST /rustfs/admin/v3/tls/certificate. The handler rejects a request
	// whose JSON body is larger than 128 KiB.
	TLSPublishBodyLimit = 128 * 1024
)

// ErrTLSPublishTooLarge reports a publication request that cannot fit the
// server's 128 KiB body limit. It is returned before the request is sent.
var ErrTLSPublishTooLarge = errors.New("TLS certificate publication exceeds the 128 KiB request body limit")

// TLSCertificateState is the payload of
// GET /rustfs/admin/v3/tls/certificate-state. Every field mirrors a live
// response; the server reports fingerprints, not parsed certificate details.
type TLSCertificateState struct {
	SchemaVersion                  string  `json:"schema_version"`
	ResourceID                     string  `json:"resource_id"`
	TargetRevision                 string  `json:"target_revision"`
	CertificateFingerprint         string  `json:"certificate_fingerprint"`
	NotAfterUnix                   int64   `json:"not_after_unix"`
	LastIssuanceID                 *string `json:"last_issuance_id"`
	ObservedCertificateFingerprint *string `json:"observed_certificate_fingerprint"`
}

// TLSCertificatePublishRequest is the body
// POST /rustfs/admin/v3/tls/certificate accepts. The server rejects unknown
// fields, so this struct must stay exact.
type TLSCertificatePublishRequest struct {
	SchemaVersion          string `json:"schema_version"`
	ResourceID             string `json:"resource_id"`
	ExpectedTargetRevision string `json:"expected_target_revision"`
	IssuanceID             string `json:"issuance_id"`
	CertificatePEM         string `json:"certificate_pem"`
	PrivateKeyPEM          string `json:"private_key_pem"`
}

// TLSCertificatePublishReceipt is the payload of a successful publication. It
// carries the new revision and the certificate the server now serves.
type TLSCertificatePublishReceipt struct {
	SchemaVersion                  string  `json:"schema_version"`
	ResourceID                     string  `json:"resource_id"`
	IssuanceID                     string  `json:"issuance_id"`
	TargetRevision                 string  `json:"target_revision"`
	CertificateFingerprint         string  `json:"certificate_fingerprint"`
	NotAfterUnix                   int64   `json:"not_after_unix"`
	ObservedCertificateFingerprint *string `json:"observed_certificate_fingerprint"`
}

// tlsCertificate issues one request against an admin route below
// /rustfs/admin/v3 and decodes the JSON response into out.
func (c *RustfsAdmin) tlsCertificate(ctx context.Context, method, relPath string, body []byte, out any) error {

	resp, err := c.doRequest(ctx, RequestData{
		Method:  method,
		RelPath: relPath,
		Content: body,
	})
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", relPath, err)
	}
	return nil
}

// GetTLSCertificateState reads the TLS certificate the server serves and the
// publication revision it was installed under
// (GET /rustfs/admin/v3/tls/certificate-state). The route is registered on
// RustFS 1.0.1 but answers 501, which is returned as an
// *UnsupportedFeatureError naming the route and the server version.
func (c *RustfsAdmin) GetTLSCertificateState(ctx context.Context) (TLSCertificateState, error) {
	var state TLSCertificateState
	err := c.tlsCertificate(ctx, http.MethodGet, "tls/certificate-state", nil, &state)
	if err != nil {
		return TLSCertificateState{}, c.featureError(ctx, err, FeatureTLSCertificate)
	}
	return state, nil
}

// PublishTLSCertificate installs certificatePEM and privateKeyPEM as the
// certificate the default listener serves (POST /rustfs/admin/v3/tls/certificate).
//
// RustFS replaces the whole certificate rather than patching it, and guards the
// replacement with a compare-and-swap on the revision it currently serves: the
// current revision is read first, and if another publisher wins the race the
// server answers 409 and that error is returned. A new issuance ID is generated
// per call; the server treats a repeated issuance ID as an idempotent retry of
// the same publication.
//
// The request must fit the server's 128 KiB body limit, otherwise
// ErrTLSPublishTooLarge is returned without contacting the server.
func (c *RustfsAdmin) PublishTLSCertificate(ctx context.Context, certificatePEM, privateKeyPEM string) (TLSCertificatePublishReceipt, error) {
	if len(certificatePEM)+len(privateKeyPEM) > TLSPublishBodyLimit {
		return TLSCertificatePublishReceipt{}, ErrTLSPublishTooLarge
	}

	state, err := c.GetTLSCertificateState(ctx)
	if err != nil {
		return TLSCertificatePublishReceipt{}, err
	}

	body, err := json.Marshal(TLSCertificatePublishRequest{
		SchemaVersion:          TLSPublishSchemaVersion,
		ResourceID:             TLSPublishResourceID,
		ExpectedTargetRevision: state.TargetRevision,
		IssuanceID:             uuid.NewString(),
		CertificatePEM:         certificatePEM,
		PrivateKeyPEM:          privateKeyPEM,
	})
	if err != nil {
		return TLSCertificatePublishReceipt{}, fmt.Errorf("encode TLS certificate publication: %w", err)
	}
	if len(body) > TLSPublishBodyLimit {
		return TLSCertificatePublishReceipt{}, ErrTLSPublishTooLarge
	}

	var receipt TLSCertificatePublishReceipt
	if err := c.tlsCertificate(ctx, http.MethodPost, "tls/certificate", body, &receipt); err != nil {
		return TLSCertificatePublishReceipt{}, c.featureError(ctx, err, FeatureTLSCertificate)
	}
	return receipt, nil
}

func init() {
	registerFeature(FeatureTLSCertificate, featureSpec{
		// Both routes ship together; the error names the pair so a 501 on
		// either side is explained by the same capability gap.
		route: "GET /rustfs/admin/v3/tls/certificate-state and POST /rustfs/admin/v3/tls/certificate",
		// Probed live: 1.0.1 registers both routes and answers 501
		// NotImplemented; 1.0.2 and 1.0.3 publish and report state.
		minVersion: &Version{Major: 1, Minor: 0, Patch: 2, Raw: "1.0.2"},
	})
}
