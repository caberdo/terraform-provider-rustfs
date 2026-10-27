// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListUsers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/list-users" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// The shape RustFS 1.0.x answers with: an object keyed by access key.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"bob":{"status":"disabled"},` +
			`"alice":{"status":"enabled","policyName":"readwrite"}}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	users, err := client.ListUsers(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	// Sorted by access key, and the key is the access key of each entry.
	if users[0].AccessKey != "alice" || users[0].Status != "enabled" || users[0].Policy != "readwrite" {
		t.Errorf("unexpected first user: %+v", users[0])
	}
	if users[1].AccessKey != "bob" || users[1].Status != "disabled" {
		t.Errorf("unexpected second user: %+v", users[1])
	}
}

func TestListUsersWithBucket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket := r.URL.Query().Get("bucket")
		if bucket != "my-bucket" {
			t.Errorf("expected bucket=my-bucket, got %s", bucket)
		}
		// A bucket with no users attached answers with an empty object.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	users, err := client.ListUsers(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("expected 0 users, got %d", len(users))
	}
	if users == nil {
		t.Error("expected an empty slice, not nil")
	}
}

func TestListUsersError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>InternalError</Code></Error>`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	if _, err := client.ListUsers(context.Background(), ""); err == nil {
		t.Fatal("expected an error for a failing list-users")
	}
}
