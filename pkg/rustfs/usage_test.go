// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

const bucketUsageTestResponse = `{
	"bucket": "my-bucket",
	"prefix": "logs/",
	"usage": {"size": 2048, "objects": 4, "versions": 6, "deleteMarkers": 1},
	"compacted": false,
	"subPrefixesPartial": true,
	"truncated": true,
	"subPrefixes": [
		{"prefix": "logs/2026/", "usage": {"size": 1024, "objects": 2, "versions": 3, "deleteMarkers": 0}}
	],
	"setsReporting": 3,
	"setsTotal": 4,
	"lastUpdateUnixSecs": 1760000001
}`

func TestGetBucketUsage(t *testing.T) {
	var gotPath, gotQuery string
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bucketUsageTestResponse))
	})

	usage, err := client.GetBucketUsage(context.Background(), "my-bucket", "logs/", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/rustfs/admin/v3/usage/my-bucket" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "prefix=logs%2F") || !strings.Contains(gotQuery, "max-entries=5") {
		t.Errorf("query = %q", gotQuery)
	}

	if usage.Bucket != "my-bucket" || usage.Prefix != "logs/" {
		t.Errorf("bucket/prefix = %q/%q", usage.Bucket, usage.Prefix)
	}
	if usage.Usage.Size != 2048 || usage.Usage.Objects != 4 || usage.Usage.Versions != 6 || usage.Usage.DeleteMarkers != 1 {
		t.Errorf("usage = %+v", usage.Usage)
	}
	if !usage.SubPrefixesPartial || !usage.Truncated || usage.Compacted {
		t.Errorf("flags = compacted=%t partial=%t truncated=%t", usage.Compacted, usage.SubPrefixesPartial, usage.Truncated)
	}
	if len(usage.SubPrefixes) != 1 || usage.SubPrefixes[0].Prefix != "logs/2026/" || usage.SubPrefixes[0].Usage.Objects != 2 {
		t.Errorf("sub prefixes = %+v", usage.SubPrefixes)
	}
	if usage.SetsReporting != 3 || usage.SetsTotal != 4 {
		t.Errorf("sets = %d/%d", usage.SetsReporting, usage.SetsTotal)
	}
	if usage.LastUpdateUnixSecs == nil || *usage.LastUpdateUnixSecs != 1760000001 {
		t.Errorf("last update = %v", usage.LastUpdateUnixSecs)
	}
	if usage.RawJSON != bucketUsageTestResponse {
		t.Errorf("raw json was not preserved")
	}
}

func TestGetBucketUsageEmpty(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query for the whole bucket, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bucket":"empty","prefix":"","usage":{"size":0,"objects":0,"versions":0,"deleteMarkers":0},"setsReporting":0,"setsTotal":4}`))
	})

	usage, err := client.GetBucketUsage(context.Background(), "empty", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.SetsReporting != 0 || usage.SetsTotal != 4 {
		t.Errorf("sets = %d/%d", usage.SetsReporting, usage.SetsTotal)
	}
	if usage.LastUpdateUnixSecs != nil {
		t.Errorf("expected no last update, got %v", *usage.LastUpdateUnixSecs)
	}
	if len(usage.SubPrefixes) != 0 {
		t.Errorf("expected no sub prefixes, got %+v", usage.SubPrefixes)
	}
}

func TestGetBucketUsageRejectsInvalidInput(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the client must not call the server for invalid input")
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, err := client.GetBucketUsage(context.Background(), "", "", 0); err == nil || !strings.Contains(err.Error(), "bucket is required") {
		t.Errorf("expected a missing bucket error, got %v", err)
	}
	if _, err := client.GetBucketUsage(context.Background(), "bucket", "", -1); err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Errorf("expected a negative max entries error, got %v", err)
	}
}

func TestGetBucketUsageServerError(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<Error><Code>InvalidArgument</Code><Message>invalid bucket name: .system</Message></Error>`))
	})

	_, err := client.GetBucketUsage(context.Background(), ".system", "", 0)
	if err == nil || !strings.Contains(err.Error(), "invalid bucket name") {
		t.Fatalf("expected the server error, got %v", err)
	}
}

func TestGetBucketUsageUnsupportedFeature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			_, _ = w.Write([]byte(`{"version":"refs/tags/1.0.3"}`))
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`))
	}))
	t.Cleanup(srv.Close)

	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})

	_, err := client.GetBucketUsage(context.Background(), "my-bucket", "", 0)
	if err == nil || !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("expected an unsupported feature error, got %v", err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.3") {
		t.Errorf("error should name the server version: %v", err)
	}
}

func TestGetBucketUsageMalformedResponse(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":`))
	})

	_, err := client.GetBucketUsage(context.Background(), "my-bucket", "", 0)
	if err == nil || !strings.Contains(err.Error(), "decode bucket usage") {
		t.Fatalf("expected a decode error, got %v", err)
	}
}
