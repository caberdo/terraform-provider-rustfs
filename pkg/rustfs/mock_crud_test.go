// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// mockAdminPath routes a request to the part of the admin API after
// /rustfs/admin/v3, which is what the handlers below switch on.
func mockAdminPath(r *http.Request) string {
	return strings.TrimPrefix(r.URL.Path, "/rustfs/admin/v3/")
}

// newMockCRUDAdminClient serves the admin endpoints the live-only CRUD tests
// exercise against a real server, so those code paths run in CI too. Payloads
// mirror the real RustFS admin API responses.
func newMockCRUDAdminClient(t *testing.T) *RustfsAdmin {
	t.Helper()
	return newHandlerAdminClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch mockAdminPath(r) {
		case "add-user":
			w.WriteHeader(http.StatusOK)
		case "user-info":
			switch r.Method {
			case http.MethodGet:
				_, _ = io.WriteString(w, `{"secretKey":"s3cret","status":"enabled","policyName":"readwrite","memberOf":["devs"]}`)
			default:
				w.WriteHeader(http.StatusOK)
			}
		case "remove-user":
			w.WriteHeader(http.StatusOK)
		case "set-user-or-group-policy":
			w.WriteHeader(http.StatusOK)
		case "set-user-secret-key", "set-user-status":
			w.WriteHeader(http.StatusOK)
		case "update-service-account":
			w.WriteHeader(http.StatusOK)
		case "delete-service-accounts":
			w.WriteHeader(http.StatusOK)
		case "add-service-accounts":
			_, _ = io.WriteString(w, `{"credentials":{"accessKey":"sa1","secretKey":"s3cret","expiration":"9999-01-01T00:00:00Z"}}`)
		case "info-service-account":
			_, _ = io.WriteString(w, `{"accessKey":"sa1","secretKey":"s3cret","name":"svc","description":"d"}`)
		case "remove-canned-policy", "add-canned-policy":
			w.WriteHeader(http.StatusOK)
		case "tier/COLD":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		default:
			switch {
			case strings.HasPrefix(mockAdminPath(r), "quota/"):
				if r.Method == http.MethodPut {
					body, _ := io.ReadAll(r.Body)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
					return
				}
				if r.Method == http.MethodGet {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"bucket":"mock-bucket","quota":1024,"quota_type":"HARD"}`)
					return
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Errorf("unexpected admin path: %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}
	})
}

// TestMockQuotaLifecycle covers ReadQuota, SetQuota and DeletQuota against a
// stubbed admin API (the live test only runs with RUSTFS_ENDPOINT set).
func TestMockQuotaLifecycle(t *testing.T) {
	client := newMockCRUDAdminClient(t)

	read, err := client.ReadQuota(context.Background(), "mock-bucket")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if read.Bucket != "mock-bucket" || read.Quota != 1024 || read.Quota_Type != "HARD" {
		t.Errorf("unexpected quota: %+v", read)
	}

	set, err := client.SetQuota(context.Background(), Quota{Bucket: "mock-bucket", Quota: 2048})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if set.Quota != 2048 || set.Quota_Type != "HARD" {
		t.Errorf("unexpected quota readback: %+v", set)
	}

	if err := client.DeletQuota(context.Background(), "mock-bucket"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// TestMockReadQuotaDecodeError covers the body decode failure of ReadQuota.
func TestMockReadQuotaDecodeError(t *testing.T) {
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `not-json`)
	})
	if _, err := client.ReadQuota(context.Background(), "b"); err == nil {
		t.Fatal("expected a decode error")
	}
}

// TestMockUserAccountLifecycle covers CreateUserAccount, ReadUserAccount and
// DeleteUserAccount against a stubbed admin API.
func TestMockUserAccountLifecycle(t *testing.T) {
	client := newMockCRUDAdminClient(t)

	if err := client.CreateUserAccount(context.Background(), UserAccount{AccessKey: "alice", SecretKey: "s3cret"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	read, err := client.ReadUserAccount(context.Background(), "alice")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if read.AccessKey != "alice" || read.Status != "enabled" || read.Policy != "readwrite" {
		t.Errorf("unexpected account: %+v", read)
	}
	if len(read.Groups) != 1 || read.Groups[0] != "devs" {
		t.Errorf("unexpected groups: %v", read.Groups)
	}

	if err := client.DeleteUserAccount(context.Background(), UserAccount{AccessKey: "alice"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// TestMockUserAccountCreatesWithPolicy covers the AttachPolicyToUser follow-up
// of CreateUserAccount.
func TestMockUserAccountCreatesWithPolicy(t *testing.T) {
	var attached []string
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch mockAdminPath(r) {
		case "add-user":
			w.WriteHeader(http.StatusOK)
		case "user-info":
			w.WriteHeader(http.StatusOK)
		case "set-user-or-group-policy":
			attached = append(attached, r.URL.Query().Get("policyName"))
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected admin path: %s", r.URL.Path)
		}
	})

	if err := client.CreateUserAccount(context.Background(), UserAccount{AccessKey: "alice", Policy: "readwrite"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(attached) != 1 || attached[0] != "readwrite" {
		t.Errorf("unexpected policy attachments: %v", attached)
	}
}

// TestMockServiceAccountLifecycle covers ReadServiceAccount and
// DeleteServiceAccount against a stubbed admin API.
func TestMockServiceAccountLifecycle(t *testing.T) {
	client := newMockCRUDAdminClient(t)

	if err := client.CreateServiceAccount(context.Background(), ServiceAccount{AccessKey: "sa1", SecretKey: "s3cret", Name: "svc"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	read, err := client.ReadServiceAccount(context.Background(), "sa1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if read.AccessKey != "sa1" || read.Name != "svc" {
		t.Errorf("unexpected service account: %+v", read)
	}

	if err := client.UpdateServiceAccount(context.Background(), ServiceAccount{AccessKey: "sa1", SecretKey: "s3cret", Name: "svc"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := client.DeleteServiceAccount(context.Background(), ServiceAccount{AccessKey: "sa1"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// TestMockReadServiceAccountDecodeError covers the body decode failure.
func TestMockReadServiceAccountDecodeError(t *testing.T) {
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `not-json`)
	})
	if _, err := client.ReadServiceAccount(context.Background(), "sa1"); err == nil {
		t.Fatal("expected a decode error")
	}
}

// TestMockEditTier covers EditTier, which the live test only reaches through
// RUSTFS_ENDPOINT, plus the request body it sends.
func TestMockEditTier(t *testing.T) {
	var gotBody string
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusOK)
	})

	if err := client.EditTier(context.Background(), "COLD", json.RawMessage(`{"tier_type":"s3"}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody != `{"tier_type":"s3"}` {
		t.Errorf("unexpected body: %s", gotBody)
	}
}

// TestMockPolicyLifecycle covers DeletePolicy and the path/query of
// CreatePolicy against a stubbed admin API.
func TestMockPolicyLifecycle(t *testing.T) {
	var gotPath, gotQuery string
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})

	if err := client.CreatePolicy(context.Background(), Policy{Name: "readwrite"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/add-canned-policy") || gotQuery != "name=readwrite" {
		t.Errorf("unexpected create request: %s?%s", gotPath, gotQuery)
	}

	if err := client.DeletePolicy(context.Background(), "readwrite"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/remove-canned-policy") || gotQuery != "name=readwrite" {
		t.Errorf("unexpected delete request: %s?%s", gotPath, gotQuery)
	}
}
