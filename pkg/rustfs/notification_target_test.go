// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newNotificationTargetTestServer(t *testing.T, handler http.HandlerFunc) *RustfsAdmin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(&RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
	return &c
}

func TestListNotificationTargets(t *testing.T) {
	client := newNotificationTargetTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/target/list" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"notify_enabled":true,"notification_endpoints":[{"account_id":"primary","service":"amqp","status":"online","health_state":"online","health_reason":"reachable","source":"persisted"}]}`))
	})
	targets, err := client.ListNotificationTargets(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	target := targets[0]
	if target.AccountID != "primary" || target.Service != "amqp" {
		t.Errorf("wrong target: %+v", target)
	}
	if target.Status != "online" || target.HealthState != "online" {
		t.Errorf("wrong target metadata: %+v", target)
	}
	if target.HealthReason != "reachable" || target.Source != "persisted" {
		t.Errorf("wrong target metadata: %+v", target)
	}
}

func TestSetNotificationTarget(t *testing.T) {
	client := newNotificationTargetTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/target/amqp/primary" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		want := `{"key_values":[{"key":"url","value":"amqps://mq:5671/"},{"key":"enable","value":"on"}]}`
		if string(body) != want {
			t.Errorf("wrong body: %s", string(body))
		}
		w.WriteHeader(http.StatusOK)
	})
	err := client.SetNotificationTarget(context.Background(), "amqp", "primary", []TargetKeyValue{
		{Key: "url", Value: "amqps://mq:5671/"},
		{Key: "enable", Value: "on"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResetNotificationTarget(t *testing.T) {
	client := newNotificationTargetTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/target/amqp/primary/reset" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := client.ResetNotificationTarget(context.Background(), "amqp", "primary"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
