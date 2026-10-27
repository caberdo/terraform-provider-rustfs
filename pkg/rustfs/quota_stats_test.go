// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

func TestGetQuotaStats(t *testing.T) {
	var gotMethod, gotPath string
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"bucket":"my-bucket","quota_limit":1048576,"current_usage":262144,`+
			`"remaining_quota":786432,"usage_percentage":25}`)
	})

	stats, err := client.GetQuotaStats(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if gotPath != "/rustfs/admin/v3/quota-stats/my-bucket" {
		t.Errorf("path = %q", gotPath)
	}
	if stats.Bucket != "my-bucket" {
		t.Errorf("bucket = %q", stats.Bucket)
	}
	if stats.QuotaLimit == nil || *stats.QuotaLimit != 1048576 {
		t.Errorf("quota limit = %v", stats.QuotaLimit)
	}
	if stats.CurrentUsage == nil || *stats.CurrentUsage != 262144 {
		t.Errorf("current usage = %v", stats.CurrentUsage)
	}
	if stats.RemainingQuota == nil || *stats.RemainingQuota != 786432 {
		t.Errorf("remaining quota = %v", stats.RemainingQuota)
	}
	if stats.UsagePercentage == nil || *stats.UsagePercentage != 25 {
		t.Errorf("usage percentage = %v", stats.UsagePercentage)
	}
}

// A field the server cannot report must stay nil so the provider can write null
// instead of inventing zero usage.
func TestGetQuotaStatsKeepsNulls(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"bucket":"my-bucket","quota_limit":null,"current_usage":null,`+
			`"remaining_quota":null,"usage_percentage":null}`)
	})

	stats, err := client.GetQuotaStats(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.QuotaLimit != nil || stats.CurrentUsage != nil || stats.RemainingQuota != nil || stats.UsagePercentage != nil {
		t.Errorf("null fields must stay nil: %+v", stats)
	}
}

func TestGetQuotaStatsErrors(t *testing.T) {
	t.Run("empty bucket", func(t *testing.T) {
		calls := 0
		client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			_, _ = io.WriteString(w, `{}`)
		})

		_, err := client.GetQuotaStats(context.Background(), "")
		if err == nil || !strings.Contains(err.Error(), "bucket name is required") {
			t.Fatalf("err = %v", err)
		}
		if calls != 0 {
			t.Errorf("the client must not call the server for an empty bucket, saw %d calls", calls)
		}
	})

	t.Run("missing bucket stays a NoSuchBucket error", func(t *testing.T) {
		client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, s3ErrorXML("NoSuchBucket", "bucket not found: missing"))
		})

		_, err := client.GetQuotaStats(context.Background(), "missing")
		if err == nil {
			t.Fatal("expected an error")
		}
		if rustfs.IsUnsupportedFeature(err) {
			t.Errorf("a semantic NoSuchBucket must not become an upgrade diagnostic: %v", err)
		}
		if !strings.Contains(err.Error(), "NoSuchBucket") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("unimplemented route becomes a capability error", func(t *testing.T) {
		client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "healthinfo") {
				_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.1"}`)
				return
			}
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, s3ErrorXML("NotImplemented", "not implemented"))
		})

		_, err := client.GetQuotaStats(context.Background(), "my-bucket")
		if !rustfs.IsUnsupportedFeature(err) {
			t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
		}
		if !strings.Contains(err.Error(), "quota-stats") {
			t.Errorf("diagnostic must name the route: %v", err)
		}
	})
}

func TestCheckQuota(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []byte
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"bucket":"my-bucket","operation_type":"PUT","operation_size":1024,`+
			`"allowed":true,"current_usage":2048,"quota_limit":4096,"remaining_quota":1024}`)
	})

	check, err := client.CheckQuota(context.Background(), "my-bucket", "PUT", 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/rustfs/admin/v3/quota-check/my-bucket" {
		t.Errorf("path = %q", gotPath)
	}

	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, gotBody)
	}
	if body["operation_type"] != "PUT" {
		t.Errorf("operation_type = %v", body["operation_type"])
	}
	if size, ok := body["operation_size"].(float64); !ok || size != 1024 {
		t.Errorf("operation_size = %v", body["operation_size"])
	}

	if !check.Allowed {
		t.Error("allowed = false, want true")
	}
	if check.OperationType != "PUT" || check.OperationSize != 1024 {
		t.Errorf("operation echo = %q/%d", check.OperationType, check.OperationSize)
	}
	if check.CurrentUsage == nil || *check.CurrentUsage != 2048 {
		t.Errorf("current usage = %v", check.CurrentUsage)
	}
	if check.QuotaLimit == nil || *check.QuotaLimit != 4096 {
		t.Errorf("quota limit = %v", check.QuotaLimit)
	}
	if check.RemainingQuota == nil || *check.RemainingQuota != 1024 {
		t.Errorf("remaining quota = %v", check.RemainingQuota)
	}
}

func TestCheckQuotaErrors(t *testing.T) {
	calls := 0
	client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{}`)
	})

	for _, tc := range []struct {
		name          string
		bucket        string
		operationType string
		size          int64
		want          string
	}{
		{name: "empty bucket", bucket: "", operationType: "PUT", size: 1, want: "bucket name is required"},
		{name: "empty operation type", bucket: "my-bucket", operationType: "", size: 1, want: "operation type is required"},
		{name: "negative size", bucket: "my-bucket", operationType: "PUT", size: -1, want: "must not be negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.CheckQuota(context.Background(), tc.bucket, tc.operationType, tc.size)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if calls != 0 {
		t.Errorf("the client must not call the server for invalid input, saw %d calls", calls)
	}

	t.Run("server error", func(t *testing.T) {
		client := newTestAdminServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, s3ErrorXML("AccessDenied", "denied by admin gate"))
		})

		_, err := client.CheckQuota(context.Background(), "my-bucket", "PUT", 1)
		if err == nil || !strings.Contains(err.Error(), "denied by admin gate") {
			t.Fatalf("err = %v", err)
		}
	})
}
