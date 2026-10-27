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

// ilmTransitionRunPayload is the body RustFS returns from an accepted async
// manual transition run, copied from the handler's response contract.
const ilmTransitionRunPayload = `{"state":"accepted","mode":"durable_job",` +
	`"job_id":"6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11",` +
	`"status_endpoint":"/rustfs/admin/v3/ilm/transition/jobs/6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11",` +
	`"cancel_endpoint":"/rustfs/admin/v3/ilm/transition/jobs/6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11",` +
	`"report":{"bucket":"logs","prefix":"archive/","tier":"WARM","dry_run":false,` +
	`"lifecycle_config_found":true,"scanned":0,"eligible":0,"enqueued":0,"dry_run_eligible":0,` +
	`"skipped_not_transition":0,"skipped_tier":0,"skipped_delete_marker":0,"skipped_directory":0,` +
	`"skipped_replication":0,"skipped_already_transitioned":0,"skipped_already_in_flight":0,` +
	`"skipped_queue_full":0,"skipped_queue_closed":0,"skipped_queue_timeout":0,` +
	`"tier_failure":0,"truncated_by_limit":false,"truncated_by_duration":false,"cancelled":false}}`

// ilmTransitionJobPayload is the body RustFS returns from
// GET /rustfs/admin/v3/ilm/transition/jobs/{job_id} for a completed job.
const ilmTransitionJobPayload = `{"status":"partial","mode":"durable_job",` +
	`"job_id":"6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11",` +
	`"status_endpoint":"/rustfs/admin/v3/ilm/transition/jobs/6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11",` +
	`"cancel_endpoint":"/rustfs/admin/v3/ilm/transition/jobs/6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11",` +
	`"cancel_requested":false,"bucket":"logs","prefix":"archive/","tier":"WARM","dry_run":false,` +
	`"created_at_unix_nanos":1700000000000000000,"updated_at_unix_nanos":1700000001000000000,` +
	`"completed_at_unix_nanos":1700000001000000000,` +
	`"report":{"bucket":"logs","prefix":"archive/","tier":"WARM","dry_run":false,` +
	`"lifecycle_config_found":true,"scanned":12,"eligible":9,"enqueued":7,"dry_run_eligible":0,` +
	`"skipped_not_transition":0,"skipped_tier":1,"skipped_delete_marker":0,"skipped_directory":0,` +
	`"skipped_replication":0,"skipped_already_transitioned":1,"skipped_already_in_flight":0,` +
	`"skipped_queue_full":3,"skipped_queue_closed":0,"skipped_queue_timeout":1,` +
	`"transition_completed":5,"transition_failed":1,"tier_failure":0,` +
	`"truncated_by_limit":true,"truncated_by_duration":false,"cancelled":false,` +
	`"continuation_token":"next-page"},` +
	`"queue_snapshot":{"queue_capacity":4,"queued":2,"active":1,"workers":2,` +
	`"queue_full":3,"queue_send_timeout":1,"compensation_pending":0,"compensation_running":0},` +
	`"failure_reason":null}`

func newILMClient(t *testing.T, handler http.HandlerFunc) *rustfs.RustfsAdmin {
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

func TestRunILMTransitionAsync(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, ilmTransitionRunPayload)
	})

	run, err := client.RunILMTransition(context.Background(), rustfs.ILMTransitionRunOptions{
		Bucket:             "logs",
		Prefix:             "archive/",
		Tier:               "WARM",
		MaxObjects:         500,
		MaxDurationSeconds: 120,
		Async:              true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost || !strings.HasSuffix(gotPath, "/rustfs/admin/v3/ilm/transition/run") {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	for _, want := range []string{
		"bucket=logs",
		"prefix=archive%2F",
		"tier=WARM",
		"maxObjects=500",
		"maxDurationSeconds=120",
		"async=true",
	} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q does not contain %q", gotQuery, want)
		}
	}
	if run.State != "accepted" || run.Mode != "durable_job" {
		t.Errorf("unexpected run: %+v", run)
	}
	if run.JobID != "6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11" {
		t.Errorf("job id = %q", run.JobID)
	}
	if run.Report.Bucket != "logs" || run.Report.Prefix != "archive/" || run.Report.Tier != "WARM" {
		t.Errorf("unexpected report: %+v", run.Report)
	}
	if !run.Report.LifecycleConfigFound {
		t.Error("expected lifecycle_config_found")
	}
}

func TestRunILMTransitionSynchronousOmitsBudgets(t *testing.T) {
	var gotQuery string
	client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"state":"completed","mode":"enqueue_only","report":{"bucket":"logs","enqueued":2}}`)
	})

	run, err := client.RunILMTransition(context.Background(), rustfs.ILMTransitionRunOptions{Bucket: "logs", DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Mode != "enqueue_only" || run.JobID != "" {
		t.Errorf("unexpected run: %+v", run)
	}
	if run.Report.Enqueued != 2 {
		t.Errorf("enqueued = %d", run.Report.Enqueued)
	}
	if strings.Contains(gotQuery, "async") || strings.Contains(gotQuery, "maxObjects") {
		t.Errorf("synchronous run sent async/budget: %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "dryRun=true") {
		t.Errorf("query %q is missing dryRun", gotQuery)
	}
}

func TestRunILMTransitionUnsupported(t *testing.T) {
	client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.1"}`)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`)
	})

	_, err := client.RunILMTransition(context.Background(), rustfs.ILMTransitionRunOptions{Bucket: "logs"})
	if !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureILMTransition {
		t.Errorf("feature = %q", unsupported.Feature)
	}
	for _, want := range []string{
		"POST /rustfs/admin/v3/ilm/transition/run",
		"RustFS 1.0.1 or later",
		"1.0.1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestILMTransitionJob(t *testing.T) {
	var gotPath string
	client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, ilmTransitionJobPayload)
	})

	job, err := client.ILMTransitionJob(context.Background(), "6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/rustfs/admin/v3/ilm/transition/jobs/6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11") {
		t.Errorf("path = %q", gotPath)
	}
	if job.Status != "partial" {
		t.Errorf("status = %q", job.Status)
	}
	if job.CompletedAtUnixNanos == nil || *job.CompletedAtUnixNanos != 1700000001000000000 {
		t.Errorf("completed_at = %v", job.CompletedAtUnixNanos)
	}
	if job.Report.SkippedQueueFull != 3 || job.Report.TransitionCompleted != 5 {
		t.Errorf("unexpected report: %+v", job.Report)
	}
	if job.Report.ContinuationToken != "next-page" {
		t.Errorf("continuation token = %q", job.Report.ContinuationToken)
	}
	if job.QueueSnapshot.QueueCapacity != 4 || job.QueueSnapshot.QueueSendTimeout != 1 {
		t.Errorf("unexpected queue snapshot: %+v", job.QueueSnapshot)
	}
}

func TestILMTransitionJobMissing(t *testing.T) {
	client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>manual transition job not found</Message></Error>`)
	})

	_, err := client.ILMTransitionJob(context.Background(), "6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11")
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

const ilmExpiryStatusPayload = `{"enabled":true,"disabled_reason":null,` +
	`"freshness":{"state":"fresh","last_cycle_end_unix_secs":1700000000,"max_expected_age_seconds":600,"reason":null},` +
	`"lifecycle_expiry":{"current_queue_capacity":8,"current_queued":2,"current_active":1,"current_workers":4,` +
	`"queue_missed":0,"scanner_queued":9,"scanner_missed":0,"scanner_blocked":0,"scanner_not_enqueued":0,"delete_failed":1},` +
	`"maintenance_control":{"primary_control":"none","sources":[{"source":"scanner","state":"idle","reason":"",` +
	`"backlog":0,"current_checked":4,"current_queued":0,"current_missed":0,"lifetime_missed":2,"partial_cycles":0}]},` +
	`"current_cycle_lifecycle_expiry_actions":3,"last_cycle_lifecycle_expiry_actions":11,` +
	`"data_movement_pause":{"paused":true,"policy":"global_pause","reasons":["decommission_active"],` +
	`"started_at_unix_secs":1699999000,"duration_seconds":1000,"operation_epoch":2,"movement_generation":7,` +
	`"movement_backlog_work_items":5,"movement_backlog_estimated":false},` +
	`"pause_backlog":{"phase":"idle"},"catch_up_estimate":{"estimated":true,"movement_work_items":5,` +
	`"dirty_usage_buckets":1,"discovered_expiry_items":3,"discovered_transition_items":4,` +
	`"undiscovered_ilm_items_known":false,"usage_baseline_unix_secs":1699999999}}`

func TestILMExpiryStatus(t *testing.T) {
	var gotPath string
	client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, ilmExpiryStatusPayload)
	})

	status, err := client.ILMExpiryStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/rustfs/admin/v3/ilm/expiry/status") {
		t.Errorf("path = %q", gotPath)
	}
	if !status.Enabled || status.Freshness.State != "fresh" {
		t.Errorf("unexpected status: %+v", status)
	}
	if status.LifecycleExpiry.DeleteFailed != 1 || status.LifecycleExpiry.CurrentWorkers != 4 {
		t.Errorf("unexpected lifecycle expiry: %+v", status.LifecycleExpiry)
	}
	if len(status.MaintenanceControl.Sources) != 1 || status.MaintenanceControl.Sources[0].LifetimeMissed != 2 {
		t.Errorf("unexpected maintenance control: %+v", status.MaintenanceControl)
	}
	if !status.DataMovementPause.Paused || len(status.DataMovementPause.Reasons) != 1 ||
		status.DataMovementPause.Reasons[0] != "decommission_active" {
		t.Errorf("unexpected pause: %+v", status.DataMovementPause)
	}
	if !status.CatchUpEstimate.Estimated || status.CatchUpEstimate.DiscoveredTransitionItems != 4 {
		t.Errorf("unexpected catch-up estimate: %+v", status.CatchUpEstimate)
	}
	if len(status.Raw) == 0 || !json.Valid(status.Raw) {
		t.Errorf("raw payload is not valid JSON: %q", status.Raw)
	}
}

func TestListILMRecoveryRecords(t *testing.T) {
	var gotQuery string
	client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"records":[{"control_id":"control-1","protocol":"transition_transaction",`+
			`"classification":"retrying","schema":"rustfs.ilm.recovery-control/v1","revision":3,`+
			`"attempt_count":2,"consecutive_failure_count":1,"first_seen_at_unix_nanos":1700000000000000000,`+
			`"first_failure_at_unix_nanos":1700000001000000000,"last_failure_at_unix_nanos":null,`+
			`"next_attempt_at_unix_nanos":1700000002000000000,"last_error_code":"source_unavailable",`+
			`"source_schema":"v1","source_generation_sha256":"aa","copy_set_sha256":"bb","source_copy_count":3}],`+
			`"next_marker":"control-1","truncated":true,"incomplete":false}`)
	})

	page, err := client.ListILMRecoveryRecords(context.Background(), rustfs.ILMRecoveryRecordQuery{
		Protocol:       "transition_transaction",
		Classification: "retrying",
		Limit:          25,
		Marker:         "control-0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"protocol=transition_transaction",
		"classification=retrying",
		"limit=25",
		"marker=control-0",
	} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q does not contain %q", gotQuery, want)
		}
	}
	if len(page.Records) != 1 {
		t.Fatalf("records = %d", len(page.Records))
	}
	record := page.Records[0]
	if record.ControlID != "control-1" || record.Classification != "retrying" {
		t.Errorf("unexpected record: %+v", record)
	}
	if record.LastFailureAtUnixNanos != nil {
		t.Errorf("last_failure_at should be nil, got %v", record.LastFailureAtUnixNanos)
	}
	if record.NextAttemptAtUnixNanos == nil || *record.NextAttemptAtUnixNanos != 1700000002000000000 {
		t.Errorf("next_attempt_at = %v", record.NextAttemptAtUnixNanos)
	}
	if page.NextMarker != "control-1" || !page.Truncated || page.Incomplete {
		t.Errorf("unexpected page: %+v", page)
	}
	if len(page.Raw) == 0 || !json.Valid(page.Raw) {
		t.Errorf("raw payload is not valid JSON: %q", page.Raw)
	}
}

func TestListILMRecoveryRecordsOmitsOptionalQuery(t *testing.T) {
	var gotQuery string
	client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"records":[],"next_marker":null,"truncated":false,"incomplete":true}`)
	})

	page, err := client.ListILMRecoveryRecords(context.Background(), rustfs.ILMRecoveryRecordQuery{Protocol: "tier_delete_journal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery != "protocol=tier_delete_journal" {
		t.Errorf("query = %q", gotQuery)
	}
	if len(page.Records) != 0 || !page.Incomplete {
		t.Errorf("unexpected page: %+v", page)
	}
}

func TestILMResponsesThatAreNotJSONFail(t *testing.T) {
	cases := []struct {
		name  string
		call  func(client *rustfs.RustfsAdmin) error
		route string
	}{
		{
			name: "run",
			call: func(client *rustfs.RustfsAdmin) error {
				_, err := client.RunILMTransition(context.Background(), rustfs.ILMTransitionRunOptions{Bucket: "logs"})
				return err
			},
		},
		{
			name: "job",
			call: func(client *rustfs.RustfsAdmin) error {
				_, err := client.ILMTransitionJob(context.Background(), "job-1")
				return err
			},
		},
		{
			name: "expiry status",
			call: func(client *rustfs.RustfsAdmin) error {
				_, err := client.ILMExpiryStatus(context.Background())
				return err
			},
		},
		{
			name: "recovery records",
			call: func(client *rustfs.RustfsAdmin) error {
				_, err := client.ListILMRecoveryRecords(context.Background(), rustfs.ILMRecoveryRecordQuery{Protocol: "transition_transaction"})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "not json")
			})
			if err := tc.call(client); err == nil {
				t.Fatal("expected a JSON decode error")
			}
		})
	}
}

// TestILMReadRequestErrorsKeepServerAnswer covers the request-error branch of
// the two ILM read routes: a denied call keeps the server's code instead of
// being reported as a missing route.
func TestILMReadRequestErrorsKeepServerAnswer(t *testing.T) {
	client := newILMClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>no</Message></Error>`)
	})

	if _, err := client.ILMExpiryStatus(context.Background()); !rustfs.IsAPIErrorCode(err, "AccessDenied") {
		t.Fatalf("expiry status error = %v, want AccessDenied", err)
	}
	if _, err := client.ListILMRecoveryRecords(context.Background(), rustfs.ILMRecoveryRecordQuery{}); !rustfs.IsAPIErrorCode(err, "AccessDenied") {
		t.Fatalf("recovery records error = %v, want AccessDenied", err)
	}
}

func TestILMReadsPropagateTruncatedBodies(t *testing.T) {
	client := newILMClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "short")
	})

	t.Run("expiry status", func(t *testing.T) {
		if _, err := client.ILMExpiryStatus(context.Background()); err == nil {
			t.Fatal("expected the truncated body to fail the read")
		}
	})
	t.Run("recovery records", func(t *testing.T) {
		if _, err := client.ListILMRecoveryRecords(context.Background(), rustfs.ILMRecoveryRecordQuery{Protocol: "transition_transaction"}); err == nil {
			t.Fatal("expected the truncated body to fail the read")
		}
	})
}
