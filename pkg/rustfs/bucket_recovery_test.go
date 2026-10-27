// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

const recoveryIncarnation = "4f8c1f3e-9a2b-4c7d-8e10-2b6d5f0a1c33"

func newBucketRecoveryClient(t *testing.T, handler http.HandlerFunc) rustfs.RustfsAdmin {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// The httptest server never verifies the signature; these values only label
	// the requests, so they are fake fixtures rather than credentials.
	return rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "unit-test-access-key",
		AccessSecret: "unit-test-secret-key",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
}

func TestRecoverOrphanedBucketRequestAndResponse(t *testing.T) {
	var method, requestPath string
	var body map[string]any

	client := newBucketRecoveryClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, requestPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"bucket":"orphaned-data","retiredIncarnationId":"`+recoveryIncarnation+`"}`)
	})

	result, err := client.RecoverOrphanedBucket(context.Background(), "orphaned-data", recoveryIncarnation)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}

	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}
	if want := "/rustfs/admin/v3/recover-orphaned-bucket/orphaned-data"; requestPath != want {
		t.Errorf("path = %q, want %q", requestPath, want)
	}
	// The server denies unknown fields, so the body must carry exactly the one
	// camelCase field.
	if len(body) != 1 {
		t.Errorf("request body = %v, want only expectedIncarnationId", body)
	}
	if got := body["expectedIncarnationId"]; got != recoveryIncarnation {
		t.Errorf("expectedIncarnationId = %v, want %q", got, recoveryIncarnation)
	}
	if result.Bucket != "orphaned-data" {
		t.Errorf("bucket = %q, want orphaned-data", result.Bucket)
	}
	if result.RetiredIncarnationID != recoveryIncarnation {
		t.Errorf("retiredIncarnationId = %q, want %q", result.RetiredIncarnationID, recoveryIncarnation)
	}
}

// TestRecoverOrphanedBucketMalformedResponse proves a 200 with a body that is
// not the documented JSON is reported as an error instead of a zero result, so
// a proxy or a future server change cannot be mistaken for a successful
// recovery that retired an empty incarnation.
func TestRecoverOrphanedBucketMalformedResponse(t *testing.T) {
	client := newBucketRecoveryClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<html>proxy error</html>`)
	})

	_, err := client.RecoverOrphanedBucket(context.Background(), "orphaned-data", recoveryIncarnation)

	if err == nil {
		t.Fatal("expected a decode error, got none")
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("a malformed 200 must not be reported as an unsupported feature: %v", err)
	}
}

// TestRecoverOrphanedBucketRouteMissingOn101 covers the 1.0.1 answer: the route
// was never registered, so the handler turns 404 into a capability diagnostic
// naming the route and the version that implements it.
func TestRecoverOrphanedBucketRouteMissingOn101(t *testing.T) {
	client := newBucketRecoveryClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/rustfs/admin/v3/healthinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.1"}`)
		case strings.Contains(r.URL.Path, "/recover-orphaned-bucket/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `Not Found`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	_, err := client.RecoverOrphanedBucket(context.Background(), "orphaned-data", recoveryIncarnation)

	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureBucketRecovery {
		t.Errorf("feature = %q, want %q", unsupported.Feature, rustfs.FeatureBucketRecovery)
	}
	if want := "POST /rustfs/admin/v3/recover-orphaned-bucket/{bucket}"; unsupported.Route != want {
		t.Errorf("route = %q, want %q", unsupported.Route, want)
	}
	if unsupported.MinVersion == nil || unsupported.MinVersion.String() != "1.0.2" {
		t.Errorf("min version = %v, want 1.0.2", unsupported.MinVersion)
	}
	if unsupported.ServerVersion != "1.0.1" {
		t.Errorf("server version = %q, want 1.0.1", unsupported.ServerVersion)
	}
	if message := err.Error(); !strings.Contains(message, "POST /rustfs/admin/v3/recover-orphaned-bucket/{bucket}") {
		t.Errorf("message %q does not name the unimplemented route", message)
	}
}

// TestRecoverOrphanedBucketRefusalStaysAnAPIError proves a 412 is not mistaken
// for a capability gap: the resource classifies it as an identity mismatch.
func TestRecoverOrphanedBucketRefusalStaysAnAPIError(t *testing.T) {
	client := newBucketRecoveryClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>PreconditionFailed</Code><Message>At least one of the pre-conditions you specified did not hold</Message></Error>`)
	})

	_, err := client.RecoverOrphanedBucket(context.Background(), "orphaned-data", recoveryIncarnation)

	if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("a 412 must not be reported as an unsupported feature: %v", err)
	}
	if !rustfs.IsAPIErrorCode(err, "PreconditionFailed") {
		t.Fatalf("expected the structured PreconditionFailed code, got %T: %v", err, err)
	}
}
