// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

// tableBucketPayload is the body RustFS 1.0.2 returns from
// GET /iceberg/v1/buckets/probe, copied from a live server.
const tableBucketPayload = `{"disable-supported":true,"table-bucket":"probe","enabled":true,` +
	`"catalog-type":"iceberg-rest","warehouse":"probe","warehouse-location":"s3://probe/",` +
	`"catalog-uri":"/iceberg/v1/probe","compat-catalog-uri":"/_iceberg/v1/probe",` +
	`"credential-vending":"unsupported","credential-scope":"warehouse-prefix",` +
	`"credential-scope-prefix":"s3://probe/","catalog-entry-present":true,"properties":{}}`

func newTableBucketClient(t *testing.T, handler http.HandlerFunc) rustfs.RustfsAdmin {
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

// TestDoDirectRequestSigningScope pins the credential scope of the requests
// that will ship. Live probes on RustFS 1.0.1, 1.0.2 and 1.0.3 accepted both
// us-east-1 and us-east-01 for PUT, GET and DELETE /iceberg/v1/buckets/<bucket>
// (raw curl --aws-sigv4 and this client's signer), so the fixed adminSigningRegion
// is safe; this test fails if the emitted scope ever changes silently.
func TestDoDirectRequestSigningScope(t *testing.T) {
	var authorization, payloadHash string
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		payloadHash = r.Header.Get("X-Amz-Content-Sha256")
		w.WriteHeader(http.StatusOK)
	})

	resp, err := client.DoDirectRequest(context.Background(), rustfs.RequestData{
		Method:  http.MethodGet,
		RelPath: "iceberg/v1/buckets/probe",
	})
	if err != nil {
		t.Fatalf("direct request failed: %v", err)
	}
	defer resp.Body.Close()

	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("unexpected Authorization header: %q", authorization)
	}
	if want := "/us-east-01/s3/aws4_request"; !strings.Contains(authorization, want) {
		t.Errorf("Authorization %q does not carry the pinned credential scope %q", authorization, want)
	}
	if payloadHash == "" {
		t.Error("direct request did not set X-Amz-Content-Sha256")
	}
}

func TestTableBucketEnableAndGet(t *testing.T) {
	var requests []string
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, tableBucketPayload)
	})

	enabled, err := client.EnableTableBucket(context.Background(), "probe")
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !enabled.Enabled || enabled.TableBucket != "probe" || enabled.CatalogType != "iceberg-rest" {
		t.Errorf("unexpected enable payload: %+v", enabled)
	}

	read, err := client.GetTableBucket(context.Background(), "probe")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if read.Warehouse != "probe" || read.CatalogURI != "/iceberg/v1/probe" || !read.CatalogEntryPresent {
		t.Errorf("unexpected get payload: %+v", read)
	}

	want := []string{"PUT /iceberg/v1/buckets/probe", "GET /iceberg/v1/buckets/probe"}
	if strings.Join(requests, ",") != strings.Join(want, ",") {
		t.Errorf("requests = %v, want %v", requests, want)
	}
}

func TestDisableTableBucketSuccess(t *testing.T) {
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"table-bucket":"probe","enabled":false,"catalog-type":"iceberg-rest",`+
			`"warehouse":"probe","catalog-entry-present":true,"catalog-uri":"/iceberg/v1/probe"}`)
	})

	disabled, err := client.DisableTableBucket(context.Background(), "probe")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Enabled {
		t.Errorf("expected the catalog switch off, got %+v", disabled)
	}
}

// TestDisableTableBucketNotImplementedOn101 covers the 1.0.1 answer the
// provider turns into a destroy warning.
func TestDisableTableBucketNotImplementedOn101(t *testing.T) {
	client := newTableBucketClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/rustfs/admin/v3/healthinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.1"}`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, `{"error":{"message":"A header you provided implies functionality that is not implemented.","type":"RESTException","code":501}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	_, err := client.DisableTableBucket(context.Background(), "probe")
	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureTableBucket {
		t.Errorf("feature = %q, want %q", unsupported.Feature, rustfs.FeatureTableBucket)
	}
	if want := "DELETE /iceberg/v1/buckets/{bucket}"; unsupported.Route != want {
		t.Errorf("route = %q, want %q", unsupported.Route, want)
	}
	if unsupported.MinVersion == nil || unsupported.MinVersion.String() != "1.0.2" {
		t.Errorf("min version = %v, want 1.0.2", unsupported.MinVersion)
	}
	if unsupported.ServerVersion != "1.0.1" {
		t.Errorf("server version = %q, want 1.0.1", unsupported.ServerVersion)
	}
	if message := err.Error(); !strings.Contains(message, "DELETE /iceberg/v1/buckets/{bucket}") {
		t.Errorf("message %q does not name the unimplemented route", message)
	}
}

// TestDisableTableBucketMissingBucketStaysAnAPIError proves the 500 for an
// absent bucket is not mistaken for a capability gap; the resource treats it as
// an already-gone object.
func TestDisableTableBucketMissingBucketStaysAnAPIError(t *testing.T) {
	client := newTableBucketClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"failed to disable table bucket: Bucket not found: probe","type":"RESTException","code":500}}`)
	})

	_, err := client.DisableTableBucket(context.Background(), "probe")
	if err == nil {
		t.Fatal("expected an error")
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("a 500 must not be reported as a capability gap: %v", err)
	}
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected the APIError with status 500, got %v", err)
	}
}

// TestTableBucketNoContentAndMalformedBody covers the empty answer DELETE and
// some GETs produce, and a body that is not valid JSON.
func TestTableBucketNoContentAndMalformedBody(t *testing.T) {
	empty := newTableBucketClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	state, err := empty.GetTableBucket(context.Background(), "probe")
	if err != nil {
		t.Fatalf("204 response: %v", err)
	}
	if state != (rustfs.TableBucket{}) {
		t.Errorf("204 response carried state: %+v", state)
	}

	malformed := newTableBucketClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{`)
	})
	if _, err := malformed.GetTableBucket(context.Background(), "probe"); err == nil {
		t.Fatal("expected a decode error for a malformed body")
	}
}
