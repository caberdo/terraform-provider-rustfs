// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const replicationDiffAggregateBody = `{` +
	`"Entries":[` +
	`{"object":"photos/a.jpg","versionId":"v1","Size":42,"deletemarker":false,` +
	`"rStatus":"PENDING","lastModified":"2026-10-20T12:00:00Z"},` +
	`{"object":"photos/deleted.jpg","Size":0,"deletemarker":true,"rStatus":"FAILED"}` +
	`],` +
	`"IsTruncated":true,"ScannedVersions":10000}`

const replicationMrfAggregateBody = `{` +
	`"Bucket":"photos",` +
	`"Targets":[{"ARN":"arn:minio:replication::peer:backup","FailedCount":3,"FailedSize":300,` +
	`"DurableCount":2,"DurableSize":200,"ObservationScope":"cluster_aggregated"}],` +
	`"TotalFailedCount":3,"TotalFailedSize":300,"QueuedCount":4,"QueuedSize":400,` +
	`"PerObjectEntriesAvailable":true,"RuntimeStatsAvailable":true,"ClusterComplete":true,` +
	`"ObservedNodeCount":2,"ExpectedNodeCount":2,"DurableBacklogAvailable":true,` +
	`"DurableCount":2,"DurableSize":200,"PerTargetDurableEntriesAvailable":true}`

func TestReplicationDiff(t *testing.T) {
	var gotMethod, gotPath, gotBucket, gotPrefix, gotAggregate string
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBucket = r.URL.Query().Get("bucket")
		gotPrefix = r.URL.Query().Get("prefix")
		gotAggregate = r.URL.Query().Get("aggregate")
		_, _ = w.Write([]byte(replicationDiffAggregateBody))
	})

	diff, err := client.ReplicationDiff(context.Background(), "photos", "photos/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/rustfs/admin/v3/replication/diff" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBucket != "photos" || gotPrefix != "photos/" || gotAggregate != "true" {
		t.Errorf("query = bucket:%q prefix:%q aggregate:%q", gotBucket, gotPrefix, gotAggregate)
	}
	if !diff.ReplicationConfigured {
		t.Error("ReplicationConfigured must be true")
	}
	if len(diff.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(diff.Entries))
	}
	first := diff.Entries[0]
	if first.Object != "photos/a.jpg" || first.VersionID == nil || *first.VersionID != "v1" {
		t.Errorf("unexpected first entry: %+v", first)
	}
	if first.Size != 42 || first.IsDeleteMarker || first.ReplicationStatus != "PENDING" {
		t.Errorf("unexpected first entry counters: %+v", first)
	}
	if first.LastModified == nil || *first.LastModified != "2026-10-20T12:00:00Z" {
		t.Errorf("unexpected last modified: %v", first.LastModified)
	}
	second := diff.Entries[1]
	if second.VersionID != nil {
		t.Errorf("missing versionId must stay nil, got %q", *second.VersionID)
	}
	if !second.IsDeleteMarker || second.ReplicationStatus != "FAILED" {
		t.Errorf("unexpected second entry: %+v", second)
	}
	if !diff.IsTruncated || diff.ScannedVersions != 10000 {
		t.Errorf("unexpected scan coverage: truncated=%v scanned=%d", diff.IsTruncated, diff.ScannedVersions)
	}
	if !strings.Contains(string(diff.Raw), `"ScannedVersions"`) {
		t.Errorf("raw body not captured: %s", diff.Raw)
	}
}

func TestReplicationDiffWithoutReplicationConfiguration(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(diagTestErrorXML("ReplicationConfigurationNotFoundError", "replication configuration not found")))
	})

	diff, err := client.ReplicationDiff(context.Background(), "photos", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff.ReplicationConfigured {
		t.Error("ReplicationConfigured must be false")
	}
	if diff.Entries == nil || len(diff.Entries) != 0 {
		t.Errorf("entries = %v, want an empty slice", diff.Entries)
	}
}

func TestReplicationDiffUnsupportedRouteBecomesFeatureError(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			_, _ = w.Write([]byte(`{"version":"refs/tags/1.0.1"}`))
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(diagTestErrorXML("NotImplemented", "not implemented")))
	})

	_, err := client.ReplicationDiff(context.Background(), "photos", "")
	if !IsUnsupportedFeature(err) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.1") || !strings.Contains(err.Error(), "replication/diff") {
		t.Errorf("diagnostic must name the version and route: %v", err)
	}
}

func TestReplicationDiffRequiresBucket(t *testing.T) {
	client := New(&RustfsAdminConfig{})
	if _, err := client.ReplicationDiff(context.Background(), "", ""); err == nil {
		t.Fatal("expected an error for an empty bucket")
	}
}

func TestReplicationMRF(t *testing.T) {
	var gotMethod, gotPath, gotBucket, gotAggregate string
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBucket = r.URL.Query().Get("bucket")
		gotAggregate = r.URL.Query().Get("aggregate")
		_, _ = w.Write([]byte(replicationMrfAggregateBody))
	})

	mrf, err := client.ReplicationMRF(context.Background(), "photos")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if gotPath != "/rustfs/admin/v3/replication/mrf" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBucket != "photos" || gotAggregate != "true" {
		t.Errorf("query = bucket:%q aggregate:%q", gotBucket, gotAggregate)
	}
	if !mrf.ReplicationConfigured {
		t.Error("ReplicationConfigured must be true")
	}
	if len(mrf.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(mrf.Targets))
	}
	target := mrf.Targets[0]
	if target.ARN != "arn:minio:replication::peer:backup" || target.FailedCount != 3 ||
		target.FailedSize != 300 || target.DurableCount != 2 || target.DurableSize != 200 ||
		target.ObservationScope != "cluster_aggregated" {
		t.Errorf("unexpected target: %+v", target)
	}
	if mrf.TotalFailedCount != 3 || mrf.TotalFailedSize != 300 {
		t.Errorf("unexpected failed totals: %+v", mrf)
	}
	if mrf.QueuedCount != 4 || mrf.QueuedSize != 400 {
		t.Errorf("unexpected queued counters: %+v", mrf)
	}
	if !mrf.PerObjectEntriesAvailable || !mrf.RuntimeStatsAvailable || !mrf.ClusterComplete {
		t.Errorf("unexpected availability flags: %+v", mrf)
	}
	if mrf.ObservedNodeCount != 2 || mrf.ExpectedNodeCount != 2 {
		t.Errorf("unexpected node counts: %+v", mrf)
	}
	if !mrf.DurableBacklogAvailable || mrf.DurableCount != 2 || mrf.DurableSize != 200 {
		t.Errorf("unexpected durable counters: %+v", mrf)
	}
	if !mrf.PerTargetDurableEntriesAvailable {
		t.Error("PerTargetDurableEntriesAvailable must be true")
	}
	if !strings.Contains(string(mrf.Raw), `"Bucket":"photos"`) {
		t.Errorf("raw body not captured: %s", mrf.Raw)
	}
}

func TestReplicationMRFWithoutReplicationConfiguration(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(diagTestErrorXML("ReplicationConfigurationNotFoundError", "replication configuration not found")))
	})

	mrf, err := client.ReplicationMRF(context.Background(), "photos")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mrf.ReplicationConfigured {
		t.Error("ReplicationConfigured must be false")
	}
	if mrf.Bucket != "photos" {
		t.Errorf("bucket = %q", mrf.Bucket)
	}
	if mrf.Targets == nil || len(mrf.Targets) != 0 {
		t.Errorf("targets = %v, want an empty slice", mrf.Targets)
	}
}

func TestReplicationMRFUnsupportedRouteBecomesFeatureError(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			_, _ = w.Write([]byte(`{"version":"refs/tags/1.0.1"}`))
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(diagTestErrorXML("NotImplemented", "not implemented")))
	})

	_, err := client.ReplicationMRF(context.Background(), "photos")
	if !IsUnsupportedFeature(err) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.1") || !strings.Contains(err.Error(), "replication/mrf") {
		t.Errorf("diagnostic must name the version and route: %v", err)
	}
}

func TestReplicationMRFRequiresBucket(t *testing.T) {
	client := New(&RustfsAdminConfig{})
	if _, err := client.ReplicationMRF(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty bucket")
	}
}

func TestReplicationDiagnosticsRejectMalformedJSON(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	})

	if _, err := client.ReplicationDiff(context.Background(), "photos", ""); err == nil {
		t.Fatal("expected a decode error for the diff")
	}
	if _, err := client.ReplicationMRF(context.Background(), "photos"); err == nil {
		t.Fatal("expected a decode error for the MRF backlog")
	}
}

func TestReplicationMRFSortsTargetsByARN(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Bucket":"photos",` +
			`"Targets":[{"ARN":"arn:zulu","FailedCount":1},{"ARN":"arn:alpha","FailedCount":2}],` +
			`"TotalFailedCount":3}`))
	})

	mrf, err := client.ReplicationMRF(context.Background(), "photos")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mrf.Targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(mrf.Targets))
	}
	if mrf.Targets[0].ARN != "arn:alpha" || mrf.Targets[1].ARN != "arn:zulu" {
		t.Errorf("targets are not sorted by ARN: %+v", mrf.Targets)
	}
}

func TestReplicationDiagnosticsPreserveSemanticErrors(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(diagTestErrorXML("NoSuchBucket", "The specified bucket does not exist")))
	})

	for name, call := range map[string]func() error{
		"diff": func() error {
			_, err := client.ReplicationDiff(context.Background(), "typo", "")
			return err
		},
		"mrf": func() error {
			_, err := client.ReplicationMRF(context.Background(), "typo")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !IsAPIErrorCode(err, "NoSuchBucket") {
				t.Errorf("semantic code was lost: %T: %v", err, err)
			}
			if IsUnsupportedFeature(err) {
				t.Errorf("a semantic 404 must not become a capability error: %v", err)
			}
		})
	}
}
