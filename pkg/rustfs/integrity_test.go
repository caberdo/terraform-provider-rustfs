// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

const integrityJobID = "0b6e6d1a-4a6b-4a5a-9a37-2f1a3c4d5e6f"

// integrityReadinessPayload is GET /v3/integrity/readiness, copied from
// crates/ecstore/src/services/integrity/model.rs.
const integrityReadinessPayload = `{
  "local_reader_supported": true,
  "write_requested": false,
  "fleet_operator_attested": false,
  "new_writes_enabled_here": false,
  "fleet_capability_verified": false,
  "unverified_requirements": [
    "peer_reader_writer_and_repair_capabilities",
    "old_process_storage_admission",
    "deployment_failure_and_performance_qualification"
  ]
}`

// integrityInventoryPayload is GET /v3/integrity/{bucket}/inventory.
const integrityInventoryPayload = `{
  "observed_at": "2026-10-21T10:11:12.123456789Z",
  "bucket_incarnation": "7f1d2c3b-4a59-4687-9a01-2b3c4d5e6f70",
  "items": [
    {
      "key": "reports/2026.csv",
      "version_id": "null",
      "data_dir": "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d",
      "size": 4096,
      "protection": "independent_commitment",
      "checksums": {"CRC32": "AAAAAA==", "SHA256": "uU0nuZNNPgilLlLX2n2r+sSE7+N6U4DukIj3rOLvzek="},
      "checksum_is_multipart": false,
      "audit_unsupported": null,
      "source_fingerprint": "a1b2c3",
      "observation_error": null
    },
    {
      "key": "reports/old.csv",
      "version_id": "1f0e9d8c-7b6a-4594-8321-0f1e2d3c4b5a",
      "data_dir": null,
      "size": 0,
      "protection": "no_payload",
      "checksums": {},
      "checksum_is_multipart": false,
      "audit_unsupported": "delete_marker",
      "source_fingerprint": "d4e5f6",
      "observation_error": null
    }
  ],
  "is_truncated": true,
  "next_key_marker": "reports/old.csv",
  "next_version_marker": "1f0e9d8c-7b6a-4594-8321-0f1e2d3c4b5a"
}`

// integrityJobPayload is the Job record returned by POST /jobs, GET /jobs/{id}
// and POST /jobs/{id}/control. A freshly created job is paused.
const integrityJobPayload = `{
  "format_version": 1,
  "id": "` + integrityJobID + `",
  "bucket": "logs",
  "bucket_incarnation": "7f1d2c3b-4a59-4687-9a01-2b3c4d5e6f70",
  "request": {
    "mode": "audit",
    "items": [
      {
        "key": "reports/2026.csv",
        "version_id": null,
        "expected_sha256": "uU0nuZNNPgilLlLX2n2r+sSE7+N6U4DukIj3rOLvzek=",
        "target_key": null
      }
    ],
    "bytes_per_second": 8388608,
    "max_object_bytes": 5368709120
  },
  "state": "paused",
  "revision": 0,
  "results": [
    {
      "state": "pending",
      "source_fingerprint": null,
      "expected_sha256": null,
      "evidence_source": null,
      "source_size": null,
      "target_version_id": null,
      "detail": null
    }
  ],
  "last_error": null
}`

func newIntegrityClient(t *testing.T, handler http.HandlerFunc) *rustfs.RustfsAdmin {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     server.Listener.Addr().String(),
		AccessKey:    "admin",
		AccessSecret: "secret",
	})
	return &client
}

func TestIntegrityReadiness(t *testing.T) {
	var gotMethod, gotPath string
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, integrityReadinessPayload)
	})

	readiness, err := client.IntegrityReadiness(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet || !strings.HasSuffix(gotPath, "/rustfs/admin/v3/integrity/readiness") {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if !readiness.LocalReaderSupported || readiness.WriteRequested || readiness.NewWritesEnabledHere {
		t.Errorf("unexpected readiness: %+v", readiness)
	}
	if len(readiness.UnverifiedRequirements) != 3 ||
		readiness.UnverifiedRequirements[0] != "peer_reader_writer_and_repair_capabilities" {
		t.Errorf("unverified requirements = %v", readiness.UnverifiedRequirements)
	}
}

func TestIntegrityInventorySendsPageParameters(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, integrityInventoryPayload)
	})

	page, err := client.IntegrityInventory(context.Background(), "logs", "reports/", "reports/a.csv", "null", 25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet || !strings.HasSuffix(gotPath, "/rustfs/admin/v3/integrity/logs/inventory") {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	for _, want := range []string{
		"prefix=reports%2F",
		"key-marker=reports%2Fa.csv",
		"version-marker=null",
		"limit=25",
	} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q does not contain %q", gotQuery, want)
		}
	}
	if page.ObservedAt != "2026-10-21T10:11:12.123456789Z" {
		t.Errorf("observed_at = %q", page.ObservedAt)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %d", len(page.Items))
	}
	first := page.Items[0]
	if first.Key != "reports/2026.csv" || first.VersionID != "null" || first.Size != 4096 {
		t.Errorf("first item = %+v", first)
	}
	if first.Protection != "independent_commitment" || first.Checksums["SHA256"] == "" {
		t.Errorf("first item checksums/protection = %+v", first)
	}
	if first.DataDir == nil || *first.DataDir != "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d" {
		t.Errorf("data_dir = %v", first.DataDir)
	}
	if page.Items[1].AuditUnsupported == nil || *page.Items[1].AuditUnsupported != "delete_marker" {
		t.Errorf("audit_unsupported = %v", page.Items[1].AuditUnsupported)
	}
	if !page.IsTruncated || page.NextKeyMarker == nil || *page.NextKeyMarker != "reports/old.csv" {
		t.Errorf("page markers = %v %v", page.IsTruncated, page.NextKeyMarker)
	}
}

func TestIntegrityInventoryOmitsEmptyParameters(t *testing.T) {
	var gotQuery string
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, integrityInventoryPayload)
	})

	if _, err := client.IntegrityInventory(context.Background(), "logs", "", "", "", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty", gotQuery)
	}
}

func TestIntegrityCreateJob(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, integrityJobPayload)
	})

	job, err := client.CreateIntegrityJob(context.Background(), "logs", rustfs.IntegrityJobRequest{
		Mode: "audit",
		Items: []rustfs.IntegrityItemRequest{{
			Key:            "reports/2026.csv",
			ExpectedSHA256: strPtr("uU0nuZNNPgilLlLX2n2r+sSE7+N6U4DukIj3rOLvzek="),
		}},
		BytesPerSecond: 8388608,
		MaxObjectBytes: 5368709120,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost || !strings.HasSuffix(gotPath, "/rustfs/admin/v3/integrity/logs/jobs") {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody["mode"] != "audit" || gotBody["bytes_per_second"].(float64) != 8388608 {
		t.Errorf("body = %v", gotBody)
	}
	items := gotBody["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["key"] != "reports/2026.csv" {
		t.Errorf("items = %v", items)
	}
	if _, ok := items[0].(map[string]any)["target_key"]; !ok {
		t.Error("target_key must be sent explicitly so audit items stay explicit")
	}
	if job.ID != integrityJobID || job.State != "paused" {
		t.Errorf("job = %+v", job)
	}
	if job.Request.Mode != "audit" || job.Request.BytesPerSecond != 8388608 {
		t.Errorf("request = %+v", job.Request)
	}
	if len(job.Results) != 1 || job.Results[0].State != "pending" {
		t.Errorf("results = %+v", job.Results)
	}
	if job.LastError != nil {
		t.Errorf("last_error = %v", job.LastError)
	}
}

func TestIntegrityGetJob(t *testing.T) {
	var gotMethod, gotPath string
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, integrityJobPayload)
	})

	job, err := client.IntegrityJob(context.Background(), "logs", integrityJobID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet || !strings.HasSuffix(gotPath, "/integrity/logs/jobs/"+integrityJobID) {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if job.Bucket != "logs" || job.BucketIncarnation == "" {
		t.Errorf("job = %+v", job)
	}
}

func TestIntegrityJobMissingIsNoSuchKey(t *testing.T) {
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>integrity job not found</Message></Error>`)
	})

	_, err := client.IntegrityJob(context.Background(), "logs", integrityJobID)
	if err == nil {
		t.Fatal("expected an error")
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("a missing job must not be reported as an unsupported route: %v", err)
	}
	if !rustfs.IsAPIErrorCode(err, "NoSuchKey") {
		t.Fatalf("expected NoSuchKey, got %v", err)
	}
}

func TestIntegrityControlJobSendsOperation(t *testing.T) {
	var gotMethod, gotPath, gotOperation string
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding control body: %v", err)
		}
		gotOperation = body["operation"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, integrityJobPayload)
	})

	if _, err := client.ControlIntegrityJob(context.Background(), "logs", integrityJobID, rustfs.IntegrityControlCancel); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost || !strings.HasSuffix(gotPath, "/integrity/logs/jobs/"+integrityJobID+"/control") {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotOperation != "cancel" {
		t.Errorf("operation = %q", gotOperation)
	}
}

func TestIntegrityJobUnsupportedRouteCarriesVersion(t *testing.T) {
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rustfs/admin/v3/integrity/logs/jobs":
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, `<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`)
		case "/rustfs/admin/v3/healthinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.3"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	_, err := client.CreateIntegrityJob(context.Background(), "logs", rustfs.IntegrityJobRequest{Mode: "audit"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("expected an unsupported feature error, got %v", err)
	}
	for _, want := range []string{"RustFS 1.0.3", "POST /rustfs/admin/v3/integrity/{bucket}/jobs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func strPtr(value string) *string { return &value }

func TestIntegrityDecodeErrors(t *testing.T) {
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "not-json")
	})

	calls := map[string]func() error{
		"readiness": func() error { _, err := client.IntegrityReadiness(context.Background()); return err },
		"inventory": func() error {
			_, err := client.IntegrityInventory(context.Background(), "logs", "", "", "", 0)
			return err
		},
		"create": func() error {
			_, err := client.CreateIntegrityJob(context.Background(), "logs", rustfs.IntegrityJobRequest{Mode: "audit"})
			return err
		},
		"status": func() error { _, err := client.IntegrityJob(context.Background(), "logs", integrityJobID); return err },
		"control": func() error {
			_, err := client.ControlIntegrityJob(context.Background(), "logs", integrityJobID, rustfs.IntegrityControlPause)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("expected a decode error")
			}
		})
	}
}

func TestIntegrityServerErrorsAreNotCapabilityGaps(t *testing.T) {
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `<Error><Code>InternalError</Code><Message>boom</Message></Error>`)
	})

	calls := map[string]func() error{
		"readiness": func() error { _, err := client.IntegrityReadiness(context.Background()); return err },
		"inventory": func() error {
			_, err := client.IntegrityInventory(context.Background(), "logs", "", "", "", 0)
			return err
		},
		"create": func() error {
			_, err := client.CreateIntegrityJob(context.Background(), "logs", rustfs.IntegrityJobRequest{Mode: "audit"})
			return err
		},
		"status": func() error { _, err := client.IntegrityJob(context.Background(), "logs", integrityJobID); return err },
		"control": func() error {
			_, err := client.ControlIntegrityJob(context.Background(), "logs", integrityJobID, rustfs.IntegrityControlPause)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("expected an error")
			}
			if rustfs.IsUnsupportedFeature(err) {
				t.Fatalf("a 500 must not be reported as a capability gap: %v", err)
			}
			if !strings.Contains(err.Error(), "boom") {
				t.Fatalf("error %q does not carry the server message", err)
			}
		})
	}
}

func TestIntegrityMethodNotAllowedIsUnsupported(t *testing.T) {
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, `<Error><Code>MethodNotAllowed</Code><Message>method not allowed</Message></Error>`)
	})

	_, err := client.IntegrityReadiness(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("expected an unsupported feature error, got %v", err)
	}
}

func TestIntegrityReadinessMissingRouteIsUnsupported(t *testing.T) {
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rustfs/admin/v3/integrity/readiness":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
		case "/rustfs/admin/v3/healthinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"1.0.1"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	_, err := client.IntegrityReadiness(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("a 404 on the readiness route must be a capability gap: %v", err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.1") {
		t.Errorf("error %q does not name the server version", err)
	}
}

func TestIntegrityTransportErrorIsNotACapabilityGap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     server.Listener.Addr().String(),
		AccessKey:    "admin",
		AccessSecret: "secret",
	})
	server.Close()

	if _, err := client.IntegrityInventory(context.Background(), "logs", "", "", "", 0); err == nil {
		t.Fatal("expected a transport error")
	} else if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("a transport error must not be a capability gap: %v", err)
	}
}

func TestIntegrityMissingBucketIsNotACapabilityGap(t *testing.T) {
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchBucket</Code><Message>no such bucket</Message></Error>`)
	})

	calls := map[string]func() error{
		"inventory": func() error {
			_, err := client.IntegrityInventory(context.Background(), "logs", "", "", "", 0)
			return err
		},
		"create": func() error {
			_, err := client.CreateIntegrityJob(context.Background(), "logs", rustfs.IntegrityJobRequest{Mode: "audit"})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("expected an error")
			}
			if rustfs.IsUnsupportedFeature(err) {
				t.Fatalf("a missing bucket must not be a capability gap: %v", err)
			}
			if !rustfs.IsAPIErrorCode(err, "NoSuchBucket") {
				t.Fatalf("expected NoSuchBucket, got %v", err)
			}
		})
	}
}

func TestIntegrityUnregisteredBucketRouteIsUnsupported(t *testing.T) {
	client := newIntegrityClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rustfs/admin/v3/integrity/logs/inventory":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchResource</Code><Message>route not found</Message></Error>`)
		case "/rustfs/admin/v3/healthinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"1.0.1"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	_, err := client.IntegrityInventory(context.Background(), "logs", "", "", "", 0)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("an unregistered route must be a capability gap: %v", err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.1") {
		t.Errorf("error %q does not name the server version", err)
	}
}
