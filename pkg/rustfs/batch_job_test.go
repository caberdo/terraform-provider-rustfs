// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestListBatchJobs(t *testing.T) {
	var gotPath, gotJobType string
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		gotPath = r.URL.Path
		gotJobType = r.URL.Query().Get("jobType")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[{"ID":"job-1","Type":"replicate","Status":"failed"}]}`))
	})

	jobs, err := client.ListBatchJobs(context.Background(), "replicate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/rustfs/admin/v3/list-jobs" {
		t.Errorf("path = %q", gotPath)
	}
	if gotJobType != "replicate" {
		t.Errorf("jobType = %q", gotJobType)
	}
	if len(jobs.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs.Jobs))
	}
	if !strings.Contains(string(jobs.Jobs[0]), `"ID":"job-1"`) {
		t.Errorf("job document does not preserve the payload: %s", jobs.Jobs[0])
	}
	if !strings.Contains(string(jobs.Raw), `"jobs"`) {
		t.Errorf("raw body not captured: %s", jobs.Raw)
	}
}

func TestListBatchJobsEmptyListIsNotNil(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jobs":[]}`))
	})

	jobs, err := client.ListBatchJobs(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jobs.Jobs == nil {
		t.Fatal("Jobs must be an empty slice, not nil")
	}
	if len(jobs.Jobs) != 0 {
		t.Fatalf("jobs = %d, want 0", len(jobs.Jobs))
	}
}

func TestListBatchJobsUnsupportedRouteBecomesFeatureError(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			_, _ = w.Write([]byte(`{"version":"refs/tags/1.0.1"}`))
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(diagTestErrorXML("NotImplemented", "not implemented")))
	})

	_, err := client.ListBatchJobs(context.Background(), "")
	if !IsUnsupportedFeature(err) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.1") {
		t.Errorf("diagnostic must name the server version: %v", err)
	}
	if !strings.Contains(err.Error(), "list-jobs") {
		t.Errorf("diagnostic must name the route: %v", err)
	}
}

func TestDescribeAndStatusBatchJob(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*RustfsAdmin, string) ([]byte, error)
		path string
	}{
		{
			name: "describe",
			call: func(c *RustfsAdmin, id string) ([]byte, error) {
				document, err := c.DescribeBatchJob(context.Background(), id)
				return document, err
			},
			path: "/rustfs/admin/v3/describe-job",
		},
		{
			name: "status",
			call: func(c *RustfsAdmin, id string) ([]byte, error) {
				document, err := c.StatusBatchJob(context.Background(), id)
				return document, err
			},
			path: "/rustfs/admin/v3/status-job",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotJobID string
			client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotJobID = r.URL.Query().Get("jobId")
				_, _ = w.Write([]byte(`{"jobId":"job/1","state":"failed"}`))
			})

			document, err := tc.call(client, "job/1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotPath != tc.path {
				t.Errorf("path = %q, want %q", gotPath, tc.path)
			}
			if gotJobID != "job/1" {
				t.Errorf("jobId = %q", gotJobID)
			}
			if !strings.Contains(string(document), `"state":"failed"`) {
				t.Errorf("document does not preserve the payload: %s", document)
			}
		})
	}
}

func TestBatchJobDocumentNotFound(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(diagTestErrorXML("NoSuchKey", "no such batch job: job-1")))
	})

	_, err := client.DescribeBatchJob(context.Background(), "job-1")
	if !IsNoSuchBatchJob(err) {
		t.Fatalf("expected a no-such-job error, got %T: %v", err, err)
	}

	_, err = client.StatusBatchJob(context.Background(), "job-1")
	if !IsNoSuchBatchJob(err) {
		t.Fatalf("expected a no-such-job error, got %T: %v", err, err)
	}
}

func TestIsNoSuchBatchJobIgnoresSubstringMatches(t *testing.T) {
	if IsNoSuchBatchJob(&APIError{Code: "AccessDenied", Message: "no such batch job"}) {
		t.Error("only the structured job codes match")
	}
	if !IsNoSuchBatchJob(&APIError{Code: "NoSuchJob"}) {
		t.Error("NoSuchJob must match")
	}
}

func TestBatchJobDocumentRequiresID(t *testing.T) {
	client := New(&RustfsAdminConfig{})

	if _, err := client.DescribeBatchJob(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty job id")
	}
	if _, err := client.StatusBatchJob(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty job id")
	}
}

func TestBatchJobEndpointsRejectMalformedAnswers(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "list-jobs") {
			_, _ = w.Write([]byte(`not json`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	if _, err := client.ListBatchJobs(context.Background(), ""); err == nil {
		t.Fatal("expected a decode error for the job list")
	}
	if _, err := client.DescribeBatchJob(context.Background(), "job-1"); err == nil {
		t.Fatal("expected an error for an empty job document")
	}
}

func TestListBatchJobsPreservesSemanticErrors(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(diagTestErrorXML("NoSuchBucket", "The specified bucket does not exist")))
	})

	_, err := client.ListBatchJobs(context.Background(), "")
	if !IsAPIErrorCode(err, "NoSuchBucket") {
		t.Errorf("semantic code was lost: %T: %v", err, err)
	}
	if IsUnsupportedFeature(err) {
		t.Errorf("a semantic 404 must not become a capability error: %v", err)
	}
}
