// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

// tlsCertificateStatePayload is the body RustFS 1.0.3 returns from
// GET /rustfs/admin/v3/tls/certificate-state for a published certificate,
// copied from a live server.
const tlsCertificateStatePayload = `{"schema_version":"rustfs.tls-publish/v1alpha1",` +
	`"resource_id":"server-default","target_revision":"1e04976a-316c-43ed-86cd-e7779b8ccd0a",` +
	`"certificate_fingerprint":"67fd508faf9701264f6385249f4cd3a269334625c20c475118ed0f8926f77e32",` +
	`"not_after_unix":1797684279,"last_issuance_id":"65f4ec47-53a7-415b-be51-1381f51adb9c",` +
	`"observed_certificate_fingerprint":"67fd508faf9701264f6385249f4cd3a269334625c20c475118ed0f8926f77e32"}`

// tlsPublishReceiptPayload is the body RustFS 1.0.3 returns from a successful
// POST /rustfs/admin/v3/tls/certificate, copied from a live server.
const tlsPublishReceiptPayload = `{"schema_version":"rustfs.tls-publish/v1alpha1",` +
	`"resource_id":"server-default","issuance_id":"65f4ec47-53a7-415b-be51-1381f51adb9c",` +
	`"target_revision":"1e04976a-316c-43ed-86cd-e7779b8ccd0a",` +
	`"certificate_fingerprint":"67fd508faf9701264f6385249f4cd3a269334625c20c475118ed0f8926f77e32",` +
	`"not_after_unix":1797684279,"observed_certificate_fingerprint":null}`

// tlsNotImplementedBody is the S3-style answer RustFS 1.0.1 gives for both TLS
// certificate routes.
const tlsNotImplementedBody = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NotImplemented</Code>` +
	`<Message>A header you provided implies functionality that is not implemented.</Message></Error>`

func TestGetTLSCertificateState(t *testing.T) {
	var requestLine string
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		requestLine = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, tlsCertificateStatePayload)
	})

	state, err := client.GetTLSCertificateState(context.Background())
	if err != nil {
		t.Fatalf("get TLS certificate state: %v", err)
	}

	if requestLine != "GET /rustfs/admin/v3/tls/certificate-state" {
		t.Errorf("request = %q", requestLine)
	}
	if state.SchemaVersion != "rustfs.tls-publish/v1alpha1" || state.ResourceID != "server-default" {
		t.Errorf("unexpected protocol fields: %+v", state)
	}
	if state.TargetRevision != "1e04976a-316c-43ed-86cd-e7779b8ccd0a" {
		t.Errorf("target revision = %q", state.TargetRevision)
	}
	if state.CertificateFingerprint != "67fd508faf9701264f6385249f4cd3a269334625c20c475118ed0f8926f77e32" {
		t.Errorf("certificate fingerprint = %q", state.CertificateFingerprint)
	}
	if state.NotAfterUnix != 1797684279 {
		t.Errorf("not_after_unix = %d", state.NotAfterUnix)
	}
	if state.LastIssuanceID == nil || *state.LastIssuanceID != "65f4ec47-53a7-415b-be51-1381f51adb9c" {
		t.Errorf("last_issuance_id = %v", state.LastIssuanceID)
	}
	if state.ObservedCertificateFingerprint == nil ||
		*state.ObservedCertificateFingerprint != state.CertificateFingerprint {
		t.Errorf("observed_certificate_fingerprint = %v", state.ObservedCertificateFingerprint)
	}
}

func TestGetTLSCertificateStateNotImplemented(t *testing.T) {
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.1"}`)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = io.WriteString(w, tlsNotImplementedBody)
	})

	_, err := client.GetTLSCertificateState(context.Background())
	if !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}

	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureTLSCertificate {
		t.Errorf("feature = %q, want %q", unsupported.Feature, rustfs.FeatureTLSCertificate)
	}
	if unsupported.ServerVersion != "1.0.1" {
		t.Errorf("server version = %q", unsupported.ServerVersion)
	}
	if unsupported.MinVersion == nil || unsupported.MinVersion.String() != "1.0.2" {
		t.Errorf("min version = %v", unsupported.MinVersion)
	}
	for _, want := range []string{
		"GET /rustfs/admin/v3/tls/certificate-state",
		"POST /rustfs/admin/v3/tls/certificate",
		"RustFS 1.0.2 or later",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestPublishTLSCertificate(t *testing.T) {
	var requests []string
	var published rustfs.TLSCertificatePublishRequest
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tls/certificate-state"):
			_, _ = io.WriteString(w, tlsCertificateStatePayload)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tls/certificate"):
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &published); err != nil {
				t.Errorf("decode publication body: %v", err)
			}
			_, _ = io.WriteString(w, tlsPublishReceiptPayload)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	receipt, err := client.PublishTLSCertificate(context.Background(), "CERT-PEM", "KEY-PEM")
	if err != nil {
		t.Fatalf("publish TLS certificate: %v", err)
	}

	if len(requests) != 2 ||
		requests[0] != "GET /rustfs/admin/v3/tls/certificate-state" ||
		requests[1] != "POST /rustfs/admin/v3/tls/certificate" {
		t.Errorf("requests = %v", requests)
	}
	if published.SchemaVersion != rustfs.TLSPublishSchemaVersion || published.ResourceID != rustfs.TLSPublishResourceID {
		t.Errorf("unexpected protocol fields: %+v", published)
	}
	if published.ExpectedTargetRevision != "1e04976a-316c-43ed-86cd-e7779b8ccd0a" {
		t.Errorf("expected_target_revision = %q", published.ExpectedTargetRevision)
	}
	if published.IssuanceID == "" || published.IssuanceID == "00000000-0000-0000-0000-000000000000" {
		t.Errorf("issuance_id = %q", published.IssuanceID)
	}
	if published.CertificatePEM != "CERT-PEM" || published.PrivateKeyPEM != "KEY-PEM" {
		t.Error("publication did not carry the certificate and key")
	}

	if receipt.SchemaVersion != rustfs.TLSPublishSchemaVersion || receipt.ResourceID != "server-default" {
		t.Errorf("unexpected receipt protocol fields: %+v", receipt)
	}
	if receipt.IssuanceID != "65f4ec47-53a7-415b-be51-1381f51adb9c" {
		t.Errorf("issuance_id = %q", receipt.IssuanceID)
	}
	if receipt.TargetRevision != "1e04976a-316c-43ed-86cd-e7779b8ccd0a" {
		t.Errorf("target_revision = %q", receipt.TargetRevision)
	}
}

func TestPublishTLSCertificateTooLarge(t *testing.T) {
	called := false
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	})

	large := strings.Repeat("A", rustfs.TLSPublishBodyLimit)
	_, err := client.PublishTLSCertificate(context.Background(), large, "KEY-PEM")
	if !errors.Is(err, rustfs.ErrTLSPublishTooLarge) {
		t.Fatalf("expected ErrTLSPublishTooLarge, got %T: %v", err, err)
	}
	if called {
		t.Error("an oversized publication must not reach the server")
	}
}

func TestGetTLSCertificateStateInvalidJSON(t *testing.T) {
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `not-json`)
	})

	_, err := client.GetTLSCertificateState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode tls/certificate-state response") {
		t.Fatalf("expected a decode error, got %v", err)
	}
}

func TestPublishTLSCertificateBodyLimitAfterEncoding(t *testing.T) {
	posts := 0
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, tlsCertificateStatePayload)
	})

	// The PEM pair fits the limit on its own, but the JSON envelope pushes the
	// request body over it.
	certificate := strings.Repeat("A", rustfs.TLSPublishBodyLimit-100)
	_, err := client.PublishTLSCertificate(context.Background(), certificate, "K")
	if !errors.Is(err, rustfs.ErrTLSPublishTooLarge) {
		t.Fatalf("expected ErrTLSPublishTooLarge, got %T: %v", err, err)
	}
	if posts != 0 {
		t.Errorf("an oversized publication must not reach the server, saw %d POSTs", posts)
	}
}

func TestPublishTLSCertificateInvalidReceipt(t *testing.T) {
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `not-json`)
			return
		}
		_, _ = io.WriteString(w, tlsCertificateStatePayload)
	})

	_, err := client.PublishTLSCertificate(context.Background(), "CERT-PEM", "KEY-PEM")
	if err == nil || !strings.Contains(err.Error(), "decode tls/certificate response") {
		t.Fatalf("expected a decode error, got %v", err)
	}
}

func TestPublishTLSCertificateSurfacesStateErrors(t *testing.T) {
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"1.0.1"}`)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = io.WriteString(w, tlsNotImplementedBody)
	})

	if _, err := client.PublishTLSCertificate(context.Background(), "CERT-PEM", "KEY-PEM"); !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("expected the state read to fail with *UnsupportedFeatureError, got %T: %v", err, err)
	}
}
