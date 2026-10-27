// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

func newTestConfigAdminServer(t *testing.T, handler http.HandlerFunc) *rustfs.RustfsAdmin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
	return &c
}

func TestGetConfig(t *testing.T) {
	var gotPath string
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(
			"# RUSTFS_NOTIFY_WEBHOOK_ENDPOINT=http://example.com/rustfs/events\n" +
				`notify_webhook:primary endpoint="http://example.com/rustfs/events" enable="off"`,
		))
	})
	kvs, err := client.GetConfig(context.Background(), "notify_webhook:primary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/get-config-kv") || !strings.Contains(gotPath, "key=notify_webhook%3Aprimary") {
		t.Errorf("wrong request: %s", gotPath)
	}
	if len(kvs) != 2 {
		t.Fatalf("expected 2 kvs, got %d: %+v", len(kvs), kvs)
	}
	if kvs[0].Key != "endpoint" || kvs[0].Value != "http://example.com/rustfs/events" {
		t.Errorf("unexpected first kv: %+v", kvs[0])
	}
	if kvs[1].Key != "enable" || kvs[1].Value != "off" {
		t.Errorf("unexpected second kv: %+v", kvs[1])
	}
}

func TestGetConfigEscapedValue(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(`notify_webhook:primary endpoint="http://example.com/a\"b\\c"`))
	})
	kvs, err := client.GetConfig(context.Background(), "notify_webhook:primary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(kvs) != 1 || kvs[0].Value != `http://example.com/a"b\c` {
		t.Errorf("unexpected kvs: %+v", kvs)
	}
}

func TestGetConfigNotFound(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("config target 'missing' not found for subsystem 'notify_webhook'"))
	})
	_, err := client.GetConfig(context.Background(), "notify_webhook:missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestSetConfig(t *testing.T) {
	var gotPath string
	var gotBody []byte
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	kvs := []rustfs.ConfigKV{{Key: "endpoint", Value: "http://example.com/rustfs/events"}}
	if err := client.SetConfig(context.Background(), "notify_webhook:primary", kvs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/set-config-kv") {
		t.Errorf("wrong path: %s", gotPath)
	}
	expected := `notify_webhook:primary endpoint="http://example.com/rustfs/events"`
	if string(gotBody) != expected {
		t.Errorf("unexpected body: %q, want %q", string(gotBody), expected)
	}
}

func TestSetConfigEscapesValue(t *testing.T) {
	var gotBody []byte
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	kvs := []rustfs.ConfigKV{{Key: "auth_token", Value: `a"b\c`}}
	if err := client.SetConfig(context.Background(), "notify_webhook", kvs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `notify_webhook auth_token="a\"b\\c"`
	if string(gotBody) != expected {
		t.Errorf("unexpected body: %q, want %q", string(gotBody), expected)
	}
}

func TestDeleteConfig(t *testing.T) {
	var gotPath string
	var gotBody []byte
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	if err := client.DeleteConfig(context.Background(), "notify_webhook:primary"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/del-config-kv") {
		t.Errorf("wrong path: %s", gotPath)
	}
	if string(gotBody) != "notify_webhook:primary" {
		t.Errorf("unexpected body: %q", string(gotBody))
	}
}

func TestListConfigHistory(t *testing.T) {
	var gotPath, gotQuery string
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"RestoreID":"4a1c","CreateTime":"2026-10-20T09:15:00Z","Data":"notify_webhook endpoint=\"*redacted*\"","DataRedacted":true},
			{"RestoreID":"3b2d","CreateTime":"2026-10-19T18:00:00Z","Data":"scanner delay=\"10\"","DataRedacted":true}
		]`))
	})

	entries, err := client.ListConfigHistory(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/rustfs/admin/v3/list-config-history-kv" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "count=7" {
		t.Errorf("query = %q, want count=7", gotQuery)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].RestoreID != "4a1c" || entries[0].CreateTime != "2026-10-20T09:15:00Z" {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if !entries[0].DataRedacted || !strings.Contains(entries[0].Data, "*redacted*") {
		t.Errorf("unexpected redaction: %+v", entries[0])
	}
}

func TestListConfigHistoryEmpty(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})

	entries, err := client.ListConfigHistory(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no entries, got %+v", entries)
	}
}

func TestListConfigHistoryRejectsInvalidCount(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the client must not call the server for an invalid count")
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, err := client.ListConfigHistory(context.Background(), 0); err == nil || !strings.Contains(err.Error(), "at least 1") {
		t.Errorf("expected a count validation error, got %v", err)
	}
}

func TestListConfigHistoryServerError(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>not authorized</Message></Error>`))
	})

	_, err := client.ListConfigHistory(context.Background(), 10)
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("expected the server error, got %v", err)
	}
}

func TestListConfigHistoryMalformedResponse(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"RestoreID":`))
	})

	_, err := client.ListConfigHistory(context.Background(), 10)
	if err == nil || !strings.Contains(err.Error(), "decode config history") {
		t.Fatalf("expected a decode error, got %v", err)
	}
}

func TestRestoreConfigHistory(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})

	if err := client.RestoreConfigHistory(context.Background(), "4a1c"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/rustfs/admin/v3/restore-config-history-kv" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "restoreId=4a1c" {
		t.Errorf("query = %q, want restoreId=4a1c", gotQuery)
	}
}

func TestRestoreConfigHistoryRejectsEmptyID(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the client must not call the server without a restore id")
		w.WriteHeader(http.StatusInternalServerError)
	})

	if err := client.RestoreConfigHistory(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "restore id is required") {
		t.Errorf("expected a restore id validation error, got %v", err)
	}
}

func TestRestoreConfigHistoryServerError(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<Error><Code>InvalidRequest</Code><Message>config history entry not found</Message></Error>`))
	})

	err := client.RestoreConfigHistory(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), "config history entry not found") {
		t.Fatalf("expected the server error, got %v", err)
	}
}

func TestListConfigHistoryUnsupportedFeature(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			_, _ = w.Write([]byte(`{"version":"refs/tags/1.0.3"}`))
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`))
	})

	_, err := client.ListConfigHistory(context.Background(), 10)
	if err == nil || !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("expected an unsupported feature error, got %v", err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.3") || !strings.Contains(err.Error(), "list-config-history-kv") {
		t.Errorf("error should name the server version and route: %v", err)
	}
}

func TestRestoreConfigHistoryUnsupportedFeature(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			_, _ = w.Write([]byte(`{"version":"refs/tags/1.0.3"}`))
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`))
	})

	err := client.RestoreConfigHistory(context.Background(), "4a1c")
	if err == nil || !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("expected an unsupported feature error, got %v", err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.3") || !strings.Contains(err.Error(), "restore-config-history-kv") {
		t.Errorf("error should name the server version and route: %v", err)
	}
}

func TestClearConfigHistory(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})

	// An empty history clears to the same empty 200, so the method must not
	// invent a failure when there is nothing to delete.
	if err := client.ClearConfigHistory(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/rustfs/admin/v3/clear-config-history-kv" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "restoreId=all" {
		t.Errorf("query = %q, want restoreId=all", gotQuery)
	}
}

func TestClearConfigHistoryServerError(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied by admin gate</Message></Error>`))
	})

	err := client.ClearConfigHistory(context.Background())
	if err == nil || !strings.Contains(err.Error(), "denied by admin gate") {
		t.Fatalf("expected the server error, got %v", err)
	}
}

func TestClearConfigHistoryUnsupportedFeature(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			_, _ = w.Write([]byte(`{"version":"refs/tags/1.0.3"}`))
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`))
	})

	err := client.ClearConfigHistory(context.Background())
	if err == nil || !rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("expected an unsupported feature error, got %v", err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.3") || !strings.Contains(err.Error(), "clear-config-history-kv") {
		t.Errorf("error should name the server version and route: %v", err)
	}
}

// A missing entry is a semantic 404 (NoSuchKey), not an unimplemented route, so
// restoring a cleared id must report the server's answer instead of asking for
// an upgrade.
func TestRestoreConfigHistoryMissingEntry(t *testing.T) {
	client := newTestConfigAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>config history entry not found</Message></Error>`))
	})

	err := client.RestoreConfigHistory(context.Background(), "cleared-entry")
	if err == nil {
		t.Fatal("expected an error")
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("a missing entry must not be reported as an unimplemented route: %v", err)
	}
	if !strings.Contains(err.Error(), "NoSuchKey") {
		t.Errorf("error should keep the server code, got %v", err)
	}
}
