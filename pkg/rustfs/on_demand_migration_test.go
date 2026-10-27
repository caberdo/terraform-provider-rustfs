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

func newODMClient(t *testing.T, handler http.HandlerFunc) rustfs.RustfsAdmin {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "unit-test-access-key",
		AccessSecret: "unit-test-secret-key",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
}

func odmTestConfig() rustfs.ODMConfig {
	session := "session-token"
	endpoint := "https://legacy.example.com:9000"
	return rustfs.ODMConfig{
		Version: 1,
		Enabled: true,
		Source: rustfs.ODMSource{
			Provider:  "minio",
			Endpoint:  &endpoint,
			Region:    "us-east-1",
			Bucket:    "legacy-photos",
			PathStyle: "auto",
			Credentials: &rustfs.ODMCredentials{
				AccessKey:    "source-access",
				SecretKey:    "source-secret",
				SessionToken: &session,
			},
			TLS: rustfs.ODMTLS{SkipVerify: false},
		},
		Filter: rustfs.ODMFilter{SourcePrefix: ptr("photos/")},
		Policy: rustfs.ODMPolicy{
			Head:                     "proxy",
			RangeGet:                 "serve_and_backfill",
			SourceError:              "propagate",
			RespectLocalDeleteMarker: true,
			PreserveETag:             true,
			EmitEvents:               true,
			NegativeCacheTTLSecs:     30,
			InlineMaxBytes:           16 * 1024 * 1024,
			MultipartPartSizeBytes:   64 * 1024 * 1024,
			MaxConcurrentPulls:       8,
			PullQueueCapacity:        1024,
			SourceTimeout:            rustfs.ODMSourceTimeout{ConnectMS: 5000, FirstByteMS: 15000, IdleMS: 30000},
		},
	}
}

func ptr[T any](value T) *T { return &value }

const odmRedactedConfigJSON = `{
  "version": 1,
  "enabled": true,
  "source": {
    "provider": "minio",
    "endpoint": "https://legacy.example.com:9000",
    "region": "us-east-1",
    "bucket": "legacy-photos",
    "path_style": "auto",
    "credentials": {"access_key": "source-access", "secret_key": "REDACTED", "session_token": "REDACTED"},
    "tls": {"skip_verify": false, "ca_cert_pem": null}
  },
  "filter": {"prefix": null, "source_prefix": "photos/"},
  "policy": {
    "head": "proxy",
    "range_get": "serve_and_backfill",
    "source_error": "propagate",
    "list_through": false,
    "respect_local_delete_marker": true,
    "preserve_etag": true,
    "copy_tags": false,
    "emit_events": true,
    "negative_cache_ttl_secs": 30,
    "inline_max_bytes": 16777216,
    "multipart_part_size_bytes": 67108864,
    "max_concurrent_pulls": 8,
    "pull_queue_capacity": 1024,
    "source_timeout": {"connect_ms": 5000, "first_byte_ms": 15000, "idle_ms": 30000},
    "bandwidth_limit_bytes_per_sec": null
  }
}`

func TestSetBucketOnDemandMigrationDryRunRequest(t *testing.T) {
	var method, path, dryRun string
	var body map[string]any

	client := newODMClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		dryRun = r.URL.Query().Get("dry-run")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"bucket":"photos","dry_run":true,"config":`+odmRedactedConfigJSON+
			`,"updated_at":null,"probe":{"reachable":true,"listable":true,"sample_key":"photos/a.jpg"}}`)
	})

	result, err := client.SetBucketOnDemandMigration(context.Background(), "photos", odmTestConfig(), true)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	if method != http.MethodPut {
		t.Errorf("method = %q, want PUT", method)
	}
	if want := "/rustfs/admin/v3/on-demand-migration/photos"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if dryRun != "true" {
		t.Errorf("dry-run = %q, want true", dryRun)
	}
	if got := body["version"]; got != float64(1) {
		t.Errorf("version = %v, want 1", got)
	}
	source, ok := body["source"].(map[string]any)
	if !ok {
		t.Fatalf("source = %v, want object", body["source"])
	}
	for _, field := range []string{"provider", "endpoint", "region", "bucket", "path_style", "credentials", "tls"} {
		if _, ok := source[field]; !ok {
			t.Errorf("source is missing %q", field)
		}
	}
	if _, ok := source["azure"]; ok {
		t.Error("an s3 source must not send an azure block")
	}
	credentials := source["credentials"].(map[string]any)
	if credentials["secret_key"] != "source-secret" || credentials["session_token"] != "session-token" {
		t.Errorf("credentials = %v, want the configured secrets", credentials)
	}

	if !result.DryRun {
		t.Error("dry_run = false, want true")
	}
	if result.UpdatedAt != nil {
		t.Errorf("updated_at = %v, want nil for a dry run", *result.UpdatedAt)
	}
	if !result.Probe.Reachable || !result.Probe.Listable || result.Probe.SampleKey == nil {
		t.Errorf("probe = %+v, want reachable/listable/sample", result.Probe)
	}
	if got := result.Config.Source.Credentials.SecretKey; got != rustfs.RedactedSecret {
		t.Errorf("response secret = %q, want redacted", got)
	}
}

func TestSetBucketOnDemandMigrationSaveOmitsDryRun(t *testing.T) {
	var query string
	client := newODMClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"bucket":"photos","dry_run":false,"config":`+odmRedactedConfigJSON+
			`,"updated_at":"2026-10-20T10:00:00Z","probe":{"reachable":true,"listable":true,"sample_key":null}}`)
	})

	result, err := client.SetBucketOnDemandMigration(context.Background(), "photos", odmTestConfig(), false)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if query != "" {
		t.Errorf("query = %q, want none", query)
	}
	if result.UpdatedAt == nil || *result.UpdatedAt != "2026-10-20T10:00:00Z" {
		t.Errorf("updated_at = %v, want the server timestamp", result.UpdatedAt)
	}
}

func TestGetBucketOnDemandMigrationReadsRedactedConfig(t *testing.T) {
	var method, path string
	client := newODMClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"bucket":"photos","config":`+odmRedactedConfigJSON+`,"updated_at":"2026-10-20T10:00:00Z"}`)
	})

	result, err := client.GetBucketOnDemandMigration(context.Background(), "photos")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if method != http.MethodGet || path != "/rustfs/admin/v3/on-demand-migration/photos" {
		t.Errorf("request = %s %s", method, path)
	}
	if result.Config.Source.Credentials.SecretKey != rustfs.RedactedSecret {
		t.Errorf("secret = %q, want REDACTED", result.Config.Source.Credentials.SecretKey)
	}
	if result.Config.Policy.InlineMaxBytes != 16777216 {
		t.Errorf("inline_max_bytes = %d, want 16777216", result.Config.Policy.InlineMaxBytes)
	}
}

func TestGetBucketOnDemandMigrationNoSuchConfiguration(t *testing.T) {
	client := newODMClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, s3ErrorXML("NoSuchConfiguration", "on-demand migration is not configured for bucket photos"))
	})

	_, err := client.GetBucketOnDemandMigration(context.Background(), "photos")
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "NoSuchConfiguration" {
		t.Errorf("code = %q, want NoSuchConfiguration", apiErr.Code)
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatal("a semantic 404 must not be reported as a missing route")
	}
}

func TestDeleteBucketOnDemandMigrationIsIdempotent(t *testing.T) {
	var method, path string
	client := newODMClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.DeleteBucketOnDemandMigration(context.Background(), "photos"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if method != http.MethodDelete || path != "/rustfs/admin/v3/on-demand-migration/photos" {
		t.Errorf("request = %s %s", method, path)
	}
}

func TestDeleteBucketOnDemandMigrationMissingBucketStaysAnAPIError(t *testing.T) {
	client := newODMClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, s3ErrorXML("NoSuchBucket", "bucket does not exist"))
	})

	err := client.DeleteBucketOnDemandMigration(context.Background(), "photos")
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "NoSuchBucket" {
		t.Fatalf("expected NoSuchBucket APIError, got %T: %v", err, err)
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatal("NoSuchBucket must not be reported as a missing route")
	}
}

func TestGetBucketOnDemandMigrationStatus(t *testing.T) {
	var path string
	client := newODMClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
  "configured": true,
  "enabled": true,
  "module_enabled": true,
  "provider": "minio",
  "endpoint_host": "legacy.example.com",
  "breaker": {"state": "closed", "opened_at": null},
  "counters": {
    "requests_total": {"GET": {"source_hit": 3, "local_hit": 7}},
    "pulled_bytes_total": 1234,
    "pulled_objects_total": {"source_hit": 3},
    "pull_failures_total": {"source_timeout": 1},
    "source_latency": {"buckets": [{"le_ms": 100, "count": 2}, {"le_ms": 500, "count": 3}], "count": 3, "sum_ms": 700}
  },
  "last_source_error": {"class": "access_denied", "at": "2026-10-20T09:00:00Z"},
  "inflight_pulls": 2,
  "queue_depth": 4,
  "served_by_source_ratio": 0.3,
  "updated_at": "2026-10-20T10:00:00Z",
  "backfill": {"job_id": "job-1", "state": "running", "listed": 10, "enqueued": 8, "pulled": 5, "skipped_existing": 2, "failed": 1, "bytes": 4096, "updated_at": "2026-10-20T10:05:00Z"}
}`)
	})

	status, err := client.GetBucketOnDemandMigrationStatus(context.Background(), "photos")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if path != "/rustfs/admin/v3/on-demand-migration/photos/status" {
		t.Errorf("path = %q", path)
	}
	if !status.Configured || !status.Enabled || !status.ModuleEnabled {
		t.Errorf("status = %+v, want configured/enabled/module enabled", status)
	}
	if status.Breaker == nil || status.Breaker.State != "closed" || status.Breaker.OpenedAt != nil {
		t.Errorf("breaker = %+v", status.Breaker)
	}
	if status.Counters == nil || status.Counters.PulledBytesTotal != 1234 {
		t.Fatalf("counters = %+v", status.Counters)
	}
	if got := status.Counters.RequestsTotal["GET"]["source_hit"]; got != 3 {
		t.Errorf("requests_total[GET][source_hit] = %d, want 3", got)
	}
	if len(status.Counters.SourceLatency.Buckets) != 2 {
		t.Errorf("latency buckets = %d, want 2", len(status.Counters.SourceLatency.Buckets))
	}
	if status.LastSourceError == nil || status.LastSourceError.Class != "access_denied" {
		t.Errorf("last_source_error = %+v", status.LastSourceError)
	}
	if status.ServedBySourceRatio == nil || *status.ServedBySourceRatio != 0.3 {
		t.Errorf("served_by_source_ratio = %v", status.ServedBySourceRatio)
	}
	if status.Backfill == nil || status.Backfill.JobID != "job-1" || status.Backfill.Failed != 1 {
		t.Errorf("backfill = %+v", status.Backfill)
	}
}

func TestOnDemandMigrationRouteMissingNamesTheFeature(t *testing.T) {
	client := newODMClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/rustfs/admin/v3/healthinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.0"}`)
		case strings.Contains(r.URL.Path, "/on-demand-migration/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `Not Found`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	_, err := client.GetBucketOnDemandMigration(context.Background(), "photos")

	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureOnDemandMigration {
		t.Errorf("feature = %q", unsupported.Feature)
	}
	if unsupported.MinVersion == nil || unsupported.MinVersion.String() != "1.0.1" {
		t.Errorf("min version = %v, want 1.0.1", unsupported.MinVersion)
	}
	if unsupported.ServerVersion != "1.0.0" {
		t.Errorf("server version = %q, want 1.0.0", unsupported.ServerVersion)
	}
}

func TestOnDemandMigrationConfigRedacted(t *testing.T) {
	config := odmTestConfig()
	azureKey := "azure-key"
	sas := "sv=1&sig=secret"
	config.Source = rustfs.ODMSource{
		Provider: "azure",
		Region:   "auto",
		Bucket:   "container",
		Azure: &rustfs.ODMAzure{
			Account:    "account",
			AccountKey: &azureKey,
			SASToken:   &sas,
		},
	}
	redacted := config.Redacted()

	if redacted.Source.Azure == nil {
		t.Fatal("azure block was dropped")
	}
	if *redacted.Source.Azure.AccountKey != rustfs.RedactedSecret {
		t.Errorf("account key = %q, want redacted", *redacted.Source.Azure.AccountKey)
	}
	if *redacted.Source.Azure.SASToken != rustfs.RedactedSecret {
		t.Errorf("sas token = %q, want redacted", *redacted.Source.Azure.SASToken)
	}
	if azureKey != "azure-key" || sas != "sv=1&sig=secret" {
		t.Error("Redacted mutated the input configuration")
	}
}

// s3ErrorXML is the S3-shaped error body the admin client parses. Keeping it
// local to this file avoids depending on provider test helpers.
func s3ErrorXML(code, message string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + code + `</Code><Message>` + message + `</Message></Error>`
}

// TestOnDemandMigrationRequestFailures covers the transport and decode error
// branches of every route: a failing request and a 2xx body that is not JSON
// both have to surface an error instead of an empty result.
func TestOnDemandMigrationRequestFailures(t *testing.T) {
	handler := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		call    func(rustfs.RustfsAdmin) error
	}{
		{
			name:    "set request fails",
			handler: handler(http.StatusInternalServerError, `{"code":"InternalError"}`),
			call: func(c rustfs.RustfsAdmin) error {
				_, err := c.SetBucketOnDemandMigration(context.Background(), "photos", odmTestConfig(), false)
				return err
			},
		},
		{
			name:    "set response is not json",
			handler: handler(http.StatusOK, `{`),
			call: func(c rustfs.RustfsAdmin) error {
				_, err := c.SetBucketOnDemandMigration(context.Background(), "photos", odmTestConfig(), false)
				return err
			},
		},
		{
			name:    "get request fails",
			handler: handler(http.StatusInternalServerError, `{"code":"InternalError"}`),
			call: func(c rustfs.RustfsAdmin) error {
				_, err := c.GetBucketOnDemandMigration(context.Background(), "photos")
				return err
			},
		},
		{
			name:    "get response is not json",
			handler: handler(http.StatusOK, `{`),
			call: func(c rustfs.RustfsAdmin) error {
				_, err := c.GetBucketOnDemandMigration(context.Background(), "photos")
				return err
			},
		},
		{
			name:    "status request fails",
			handler: handler(http.StatusInternalServerError, `{"code":"InternalError"}`),
			call: func(c rustfs.RustfsAdmin) error {
				_, err := c.GetBucketOnDemandMigrationStatus(context.Background(), "photos")
				return err
			},
		},
		{
			name:    "status response is not json",
			handler: handler(http.StatusOK, `{`),
			call: func(c rustfs.RustfsAdmin) error {
				_, err := c.GetBucketOnDemandMigrationStatus(context.Background(), "photos")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(newODMClient(t, tc.handler)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// TestOnDemandMigrationConfigRedactedCredentialsAndGCS covers the credential
// and GCS copies that TestOnDemandMigrationConfigRedacted does not reach.
func TestOnDemandMigrationConfigRedactedCredentialsAndGCS(t *testing.T) {
	config := odmTestConfig()
	if config.Source.Credentials == nil || config.Source.Credentials.SessionToken == nil {
		t.Fatal("fixture must carry source credentials and a session token")
	}
	accessKey := config.Source.Credentials.AccessKey
	secretKey := config.Source.Credentials.SecretKey
	sessionToken := *config.Source.Credentials.SessionToken

	redacted := config.Redacted()
	if redacted.Source.Credentials == nil {
		t.Fatal("credentials block was dropped")
	}
	if got := redacted.Source.Credentials.SecretKey; got != rustfs.RedactedSecret {
		t.Errorf("secret key = %q, want redacted", got)
	}
	if redacted.Source.Credentials.SessionToken == nil || *redacted.Source.Credentials.SessionToken != rustfs.RedactedSecret {
		t.Error("session token was not redacted")
	}
	if got := redacted.Source.Credentials.AccessKey; got != accessKey {
		t.Errorf("access key = %q, want it preserved", got)
	}
	if config.Source.Credentials.SecretKey != secretKey || *config.Source.Credentials.SessionToken != sessionToken {
		t.Error("Redacted mutated the input configuration")
	}

	serviceAccount := `{"type":"service_account"}`
	gcsConfig := rustfs.ODMConfig{Source: rustfs.ODMSource{
		GCS: &rustfs.ODMGCS{ServiceAccountJSON: serviceAccount},
	}}
	redactedGCS := gcsConfig.Redacted()
	if redactedGCS.Source.GCS == nil {
		t.Fatal("gcs block was dropped")
	}
	if got := redactedGCS.Source.GCS.ServiceAccountJSON; got != rustfs.RedactedSecret {
		t.Errorf("service account json = %q, want redacted", got)
	}
	if gcsConfig.Source.GCS.ServiceAccountJSON != serviceAccount {
		t.Error("Redacted mutated the input configuration")
	}
}
