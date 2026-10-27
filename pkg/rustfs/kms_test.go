// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetKmsStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/status" {
			t.Errorf("expected /rustfs/admin/v3/kms/status, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"backend_type": "local",
			"backend_status": "healthy",
			"cache_enabled": true,
			"cache_stats": {
				"hit_count": 1,
				"miss_count": 2,
				"entry_count": 3,
				"eviction_count": 4
			},
			"default_key_id": "key-01",
			"capabilities": {
				"encrypt": true,
				"decrypt": true
			},
			"cluster_config": {
				"consistent": true,
				"nodes": [
					{"host": "local", "config_fingerprint": "abc123", "error": null}
				]
			}
		}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	status, err := client.GetKmsStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.BackendType != "local" {
		t.Errorf("expected local, got %s", status.BackendType)
	}
	if status.BackendStatus != "healthy" {
		t.Errorf("expected healthy, got %s", status.BackendStatus)
	}
	if !status.CacheEnabled {
		t.Error("expected cache_enabled true")
	}
	if status.CacheStats == nil {
		t.Fatal("expected cache_stats")
	}
	if status.CacheStats.HitCount != 1 || status.CacheStats.MissCount != 2 ||
		status.CacheStats.EntryCount != 3 || status.CacheStats.EvictionCount != 4 {
		t.Errorf("unexpected cache_stats: %+v", status.CacheStats)
	}
	if status.DefaultKeyID == nil || *status.DefaultKeyID != "key-01" {
		t.Errorf("expected key-01, got %v", status.DefaultKeyID)
	}
	if !status.Capabilities["encrypt"] || !status.Capabilities["decrypt"] {
		t.Errorf("unexpected capabilities: %v", status.Capabilities)
	}
	if status.ClusterConfig == nil || !status.ClusterConfig.Consistent {
		t.Fatal("expected cluster_config with consistent=true")
	}
	if len(status.ClusterConfig.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(status.ClusterConfig.Nodes))
	}
	node := status.ClusterConfig.Nodes[0]
	if node.Host != "local" {
		t.Errorf("expected host local, got %s", node.Host)
	}
	if node.ConfigFingerprint == nil || *node.ConfigFingerprint != "abc123" {
		t.Errorf("unexpected config_fingerprint: %v", node.ConfigFingerprint)
	}
	if node.Error != nil {
		t.Errorf("expected nil error, got %v", node.Error)
	}
}

func TestGetKmsStatusOptionalFieldsAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"backend_type": "aws",
			"backend_status": "error",
			"cache_enabled": false,
			"cache_stats": null,
			"default_key_id": null
		}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	status, err := client.GetKmsStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.BackendType != "aws" {
		t.Errorf("expected aws, got %s", status.BackendType)
	}
	if status.BackendStatus != "error" {
		t.Errorf("expected error, got %s", status.BackendStatus)
	}
	if status.CacheEnabled {
		t.Error("expected cache_enabled false")
	}
	if status.CacheStats != nil {
		t.Errorf("expected nil cache_stats, got %+v", status.CacheStats)
	}
	if status.DefaultKeyID != nil {
		t.Errorf("expected nil default_key_id, got %v", status.DefaultKeyID)
	}
	if status.Capabilities != nil {
		t.Errorf("expected nil capabilities, got %v", status.Capabilities)
	}
	if status.ClusterConfig != nil {
		t.Errorf("expected nil cluster_config, got %+v", status.ClusterConfig)
	}
}

func TestGetKmsConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/config" {
			t.Errorf("expected /rustfs/admin/v3/kms/config, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"backend": "vault-kv2",
			"cache_enabled": true,
			"cache_max_keys": 1000,
			"cache_ttl_seconds": 300,
			"default_key_id": "key-01"
		}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	config, err := client.GetKmsConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Backend != "vault-kv2" {
		t.Errorf("expected vault-kv2, got %s", config.Backend)
	}
	if !config.CacheEnabled {
		t.Error("expected cache_enabled true")
	}
	if config.CacheMaxKeys != 1000 {
		t.Errorf("expected 1000, got %d", config.CacheMaxKeys)
	}
	if config.CacheTTLSeconds != 300 {
		t.Errorf("expected 300, got %d", config.CacheTTLSeconds)
	}
	if config.DefaultKeyID == nil || *config.DefaultKeyID != "key-01" {
		t.Errorf("expected key-01, got %v", config.DefaultKeyID)
	}
}

func TestGetKmsConfigNullDefaultKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"backend": "local",
			"cache_enabled": false,
			"cache_max_keys": 0,
			"cache_ttl_seconds": 0,
			"default_key_id": null
		}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	config, err := client.GetKmsConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.DefaultKeyID != nil {
		t.Errorf("expected nil default_key_id, got %v", config.DefaultKeyID)
	}
}

func TestGetKmsStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("KMS status not available"))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	if _, err := client.GetKmsStatus(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	} else if !strings.Contains(err.Error(), "KMS status not available") {
		t.Errorf("expected error containing 'KMS status not available', got %v", err)
	}
}

func TestGetKmsConfigError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("KMS config not available"))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	if _, err := client.GetKmsConfig(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	} else if !strings.Contains(err.Error(), "KMS config not available") {
		t.Errorf("expected error containing 'KMS config not available', got %v", err)
	}
}

func newKmsTestClient(t *testing.T, handler http.HandlerFunc) *RustfsAdmin {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Date") == "" {
			t.Error("expected signed request headers")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"
	return &client
}

func readBody(t *testing.T, body io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	return string(b)
}

func TestCreateKmsKey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var body struct {
			Tags map[string]string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if body.Tags["name"] != "mykey" {
			t.Errorf("wrong name tag: %s", body.Tags["name"])
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"key created successfully","key_id":"mykey","key_metadata":{"key_id":"mykey","key_state":"Enabled","key_usage":"EncryptDecrypt","creation_date":"2026-09-07T00:00:00Z","origin":"KMS","key_manager":"CUSTOMER","tags":{"name":"mykey"}}}`))
	})

	key, err := client.CreateKmsKey(context.Background(), "mykey")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.KeyID != "mykey" {
		t.Errorf("wrong key id: %s", key.KeyID)
	}
	if key.KeyState != "Enabled" {
		t.Errorf("wrong key state: %s", key.KeyState)
	}
	if key.Tags["name"] != "mykey" {
		t.Errorf("missing name tag: %+v", key.Tags)
	}
}

func TestDescribeKmsKey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/k1" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"Key described successfully","key_metadata":{"key_id":"k1","key_state":"Disabled","key_usage":"EncryptDecrypt","creation_date":"2026-09-07T00:00:00Z","origin":"KMS","key_manager":"CUSTOMER","tags":{"name":"k1"}},"impact":null}`))
	})

	key, err := client.DescribeKmsKey(context.Background(), "k1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.KeyID != "k1" {
		t.Errorf("wrong key id: %s", key.KeyID)
	}
	if key.KeyState != "Disabled" {
		t.Errorf("wrong key state: %s", key.KeyState)
	}
}

func TestListKmsKeys(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"keys listed successfully","keys":[{"key_id":"k1","status":"Active","created_at":"2026-09-07T00:00:00Z","created_by":"local-kms"},{"key_id":"k2","status":"Active","created_at":"2026-09-07T00:00:00Z"}],"truncated":false,"next_marker":null}`))
	})

	keys, err := client.ListKmsKeys(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0].KeyID != "k1" || keys[1].KeyID != "k2" {
		t.Errorf("wrong keys: %+v", keys)
	}
	if keys[0].CreatedBy == nil || *keys[0].CreatedBy != "local-kms" {
		t.Errorf("wrong created_by: %+v", keys[0].CreatedBy)
	}
}

func TestEnableKmsKey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/enable" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		body := readBody(t, r.Body)
		var req struct {
			KeyID string `json:"key_id"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if req.KeyID != "mykey" {
			t.Errorf("wrong key id: %s", req.KeyID)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"key enabled successfully","key_id":"mykey","key_metadata":null}`))
	})

	if err := client.EnableKmsKey(context.Background(), "mykey"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDisableKmsKey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/disable" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var req struct {
			KeyID string `json:"key_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if req.KeyID != "mykey" {
			t.Errorf("wrong key id: %s", req.KeyID)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"key disabled successfully","key_id":"mykey","key_metadata":null}`))
	})

	if err := client.DisableKmsKey(context.Background(), "mykey"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRotateKmsKey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/rotate" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var req struct {
			KeyID string `json:"key_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if req.KeyID != "mykey" {
			t.Errorf("wrong key id: %s", req.KeyID)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"key rotated successfully","key_id":"mykey","key_metadata":{"key_id":"mykey","key_state":"Enabled","creation_date":"2026-09-07T01:00:00Z","origin":"KMS","key_manager":"CUSTOMER"}}`))
	})

	key, err := client.RotateKmsKey(context.Background(), "mykey")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.KeyID != "mykey" {
		t.Errorf("wrong key id: %s", key.KeyID)
	}
}

func TestDeleteKmsKey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/delete" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var req struct {
			KeyID string `json:"key_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if req.KeyID != "mykey" {
			t.Errorf("wrong key id: %s", req.KeyID)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"key deleted successfully","key_id":"mykey","deletion_date":"2026-10-07T00:00:00Z","impact":null}`))
	})

	if err := client.DeleteKmsKey(context.Background(), "mykey"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateKmsKeyDescription(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/update-description" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var req struct {
			KeyID       string `json:"key_id"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if req.KeyID != "k1" || req.Description != "app key" {
			t.Errorf("unexpected body: %+v", req)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"key description updated","key_id":"k1","key_metadata":{"key_id":"k1","key_state":"Enabled","description":"app key","tags":{"name":"k1"}}}`))
	})

	key, err := client.UpdateKmsKeyDescription(context.Background(), "k1", "app key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.Description == nil || *key.Description != "app key" {
		t.Errorf("unexpected description: %v", key.Description)
	}
}

func TestTagKmsKey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/tag" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var req struct {
			KeyID string            `json:"key_id"`
			Tags  map[string]string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if req.KeyID != "k1" || req.Tags["environment"] != "test" {
			t.Errorf("unexpected body: %+v", req)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"key tags updated","key_id":"k1","key_metadata":{"key_id":"k1","key_state":"Enabled","tags":{"name":"k1","environment":"test"}}}`))
	})

	key, err := client.TagKmsKey(context.Background(), "k1", map[string]string{"environment": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.Tags["environment"] != "test" {
		t.Errorf("unexpected tags: %+v", key.Tags)
	}
}

func TestUntagKmsKey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/untag" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var req struct {
			KeyID   string   `json:"key_id"`
			TagKeys []string `json:"tag_keys"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if req.KeyID != "k1" || len(req.TagKeys) != 1 || req.TagKeys[0] != "environment" {
			t.Errorf("unexpected body: %+v", req)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"message":"key tags removed","key_id":"k1","key_metadata":{"key_id":"k1","key_state":"Enabled","tags":{"name":"k1"}}}`))
	})

	key, err := client.UntagKmsKey(context.Background(), "k1", []string{"environment"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.Tags["environment"]; ok {
		t.Errorf("expected the tag to be gone: %+v", key.Tags)
	}
}

func TestDeleteKmsKeyWithWindow(t *testing.T) {
	window := int64(7)
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/delete" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var req struct {
			KeyID               string `json:"key_id"`
			PendingWindowInDays *int64 `json:"pending_window_in_days"`
			ForceImmediate      *bool  `json:"force_immediate"`
			ConfirmKeyID        string `json:"confirm_key_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if req.KeyID != "k1" {
			t.Errorf("wrong key id: %s", req.KeyID)
		}
		if req.PendingWindowInDays == nil || *req.PendingWindowInDays != window {
			t.Errorf("wrong window: %v", req.PendingWindowInDays)
		}
		if req.ForceImmediate != nil || req.ConfirmKeyID != "" {
			t.Errorf("scheduled deletion must not carry immediate-deletion fields: %+v", req)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"key_id":"k1","deletion_date":"2026-10-27T00:00:00Z"}`))
	})

	if err := client.DeleteKmsKeyWithWindow(context.Background(), "k1", &window); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestKmsCapabilityError(t *testing.T) {
	t.Run("501 names the backend capability", func(t *testing.T) {
		client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(`{"error":"the configured KMS backend does not support rotation"}`))
		})
		_, err := client.RotateKmsKey(context.Background(), "k1")
		if err == nil || !strings.Contains(err.Error(), "does not support key rotation") {
			t.Fatalf("expected a capability error, got %v", err)
		}
	})

	t.Run("non-501 is unchanged", func(t *testing.T) {
		client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`boom`))
		})
		_, err := client.RotateKmsKey(context.Background(), "k1")
		if err == nil || strings.Contains(err.Error(), "does not support") {
			t.Fatalf("expected the raw error, got %v", err)
		}
	})
}

func TestStartKmsRekey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/rekey" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var req struct {
			Buckets []string `json:"buckets"`
			Prefix  string   `json:"prefix"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if len(req.Buckets) != 1 || req.Buckets[0] != "b1" || req.Prefix != "photos/" {
			t.Errorf("unexpected body: %+v", req)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"job_id":"job-1","state":"running","buckets":["b1"],"current_bucket":"b1","scanned":10,"rewrapped":7,"already_current":1,"not_applicable":1,"failed":1}`))
	})

	status, err := client.StartKmsRekey(context.Background(), []string{"b1"}, "photos/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.JobID != "job-1" || status.State != "running" {
		t.Errorf("unexpected status: %+v", status)
	}
	if status.Scanned != 10 || status.Rewrapped != 7 || status.AlreadyCurrent != 1 || status.NotApplicable != 1 || status.Failed != 1 {
		t.Errorf("unexpected counters: %+v", status)
	}
	if status.CurrentBucket == nil || *status.CurrentBucket != "b1" {
		t.Errorf("unexpected current bucket: %v", status.CurrentBucket)
	}
}

func TestStartKmsRekeyAllBucketsOmitsScope(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if len(req) != 0 {
			t.Errorf("expected an empty body, got %+v", req)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"job_id":"job-1","state":"running","buckets":["b1"],"scanned":0,"rewrapped":0,"already_current":0,"not_applicable":0,"failed":0}`))
	})

	if _, err := client.StartKmsRekey(context.Background(), nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetKmsRekeyStatus(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/rekey/status" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"job_id":"job-2","state":"completed","buckets":["b1","b2"],"current_bucket":null,"scanned":20,"rewrapped":18,"already_current":2,"not_applicable":0,"failed":0}`))
	})

	status, err := client.GetKmsRekeyStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.JobID != "job-2" || status.State != "completed" {
		t.Errorf("unexpected status: %+v", status)
	}
	if status.CurrentBucket != nil {
		t.Errorf("expected no current bucket, got %v", *status.CurrentBucket)
	}
}

func TestGetKmsRekeyStatusNoSweep(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no rekey sweep has run"}`))
	})

	_, err := client.GetKmsRekeyStatus(context.Background())
	if !errors.Is(err, ErrKmsRekeyNotRun) {
		t.Fatalf("expected ErrKmsRekeyNotRun, got %v", err)
	}
}

func TestCancelKmsRekey(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/keys/rekey/cancel" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"job_id":"job-1","state":"cancelled","buckets":["b1"],"scanned":3,"rewrapped":1,"already_current":0,"not_applicable":2,"failed":0}`))
	})

	status, err := client.CancelKmsRekey(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "cancelled" {
		t.Errorf("unexpected state: %s", status.State)
	}
}

func TestStartKmsRekeyUnsupportedBackend(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"the configured KMS backend does not support rewrapping data-key envelopes"}`))
	})

	_, err := client.StartKmsRekey(context.Background(), nil, "")
	if err == nil || !strings.Contains(err.Error(), "does not support rewrapping data-key envelopes") {
		t.Fatalf("expected a capability error, got %v", err)
	}
}

func TestStartKmsRekeyAlreadyRunning(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"a rekey sweep is already running","job_id":"job-1"}`))
	})

	_, err := client.StartKmsRekey(context.Background(), nil, "")
	if err == nil || !strings.Contains(err.Error(), "already running") || !strings.Contains(err.Error(), "job-1") {
		t.Fatalf("expected an already-running error naming the job, got %v", err)
	}
}

func TestGetKmsBackupStatus(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/backup" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service_status":"healthy","backend":"local","responsibility":"full-material","export_supported":true,"deployment_identity":"deploy-1","backup_root":"/var/lib/rustfs/kms-backups","backup_kek_configured":true,"backup_kek_id":"kek-1","backup_kek_version":3,"target_key_dir":"/var/lib/rustfs/kms","unavailable_reason":null}`))
	})

	status, err := client.GetKmsBackupStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.ExportSupported || status.ServiceStatus != "healthy" {
		t.Errorf("unexpected status: %+v", status)
	}
	if status.Backend == nil || *status.Backend != "local" {
		t.Errorf("unexpected backend: %v", status.Backend)
	}
	if status.Responsibility == nil || *status.Responsibility != "full-material" {
		t.Errorf("unexpected responsibility: %v", status.Responsibility)
	}
	if status.BackupKEKVersion == nil || *status.BackupKEKVersion != 3 {
		t.Errorf("unexpected kek version: %v", status.BackupKEKVersion)
	}
	if status.UnavailableReason != nil {
		t.Errorf("expected no unavailable reason, got %v", *status.UnavailableReason)
	}
}

func TestGetKmsBackupStatusOptionalAbsent(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service_status":"unavailable","backend":null,"responsibility":null,"export_supported":false,"deployment_identity":null,"backup_root":null,"backup_kek_configured":false,"backup_kek_id":null,"backup_kek_version":null,"target_key_dir":null,"unavailable_reason":"RUSTFS_KMS_BACKUP_DIR is not set"}`))
	})

	status, err := client.GetKmsBackupStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.ExportSupported {
		t.Error("expected export_supported false")
	}
	if status.UnavailableReason == nil || *status.UnavailableReason == "" {
		t.Error("expected the unavailable reason")
	}
}

func TestCreateKmsBackup(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/kms/backup" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		var req struct {
			BackupID string `json:"backup_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if req.BackupID != "nightly-1" {
			t.Errorf("wrong backup id: %s", req.BackupID)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"backup_id":"nightly-1","snapshot_generation":42,"deployment_identity":"deploy-1","artifact_count":3,"manifest_digest":"abc123","backup_kek_id":"kek-1","backup_kek_version":3,"sanitized_config_included":true}`))
	})

	bundle, err := client.CreateKmsBackup(context.Background(), "nightly-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.BackupID != "nightly-1" || bundle.SnapshotGeneration != 42 {
		t.Errorf("unexpected bundle: %+v", bundle)
	}
	if bundle.ArtifactCount != 3 || bundle.ManifestDigest != "abc123" {
		t.Errorf("unexpected metadata: %+v", bundle)
	}
	if bundle.BackupKEKVersion != 3 || !bundle.SanitizedConfigIncluded {
		t.Errorf("unexpected bundle fields: %+v", bundle)
	}
}

func TestCreateKmsBackupUnsupportedBackend(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"message":"backend aws cannot export a RustFS bundle"}`))
	})

	_, err := client.CreateKmsBackup(context.Background(), "nightly-1")
	if err == nil || !strings.Contains(err.Error(), "does not support KMS backup") {
		t.Fatalf("expected a capability error, got %v", err)
	}
}

func TestGetKmsRekeyStatusInvalidJSON(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	})

	if _, err := client.GetKmsRekeyStatus(context.Background()); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestGetKmsBackupStatusError(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	})

	if _, err := client.GetKmsBackupStatus(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected the server error, got %v", err)
	}
}

func TestGetKmsBackupStatusInvalidJSON(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service_status":`))
	})

	if _, err := client.GetKmsBackupStatus(context.Background()); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestCreateKmsBackupInvalidJSON(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	})

	if _, err := client.CreateKmsBackup(context.Background(), "nightly-1"); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestKmsKeyMetadataMutationErrors(t *testing.T) {
	t.Run("server error", func(t *testing.T) {
		client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`boom`))
		})
		if _, err := client.TagKmsKey(context.Background(), "k1", map[string]string{"env": "test"}); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("expected the server error, got %v", err)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not-json`))
		})
		if _, err := client.TagKmsKey(context.Background(), "k1", nil); err == nil {
			t.Fatal("expected a decode error")
		}
	})

	t.Run("missing key metadata reports the server message", func(t *testing.T) {
		client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":false,"message":"key not found"}`))
		})
		_, err := client.TagKmsKey(context.Background(), "k1", nil)
		if err == nil || err.Error() != "key not found" {
			t.Fatalf("expected the server message, got %v", err)
		}
	})
}

func TestKmsRekeyCallMarshalError(t *testing.T) {
	client := newKmsTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("no request should be sent when the body cannot be encoded")
	})

	if _, err := client.kmsRekeyCall(context.Background(), http.MethodPost, "kms/keys/rekey", make(chan int)); err == nil {
		t.Fatal("expected a marshal error")
	}
}

func TestKmsKeyMetadataMutationMarshalError(t *testing.T) {
	client := newKmsTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("no request should be sent when the body cannot be encoded")
	})

	if _, err := client.kmsKeyMetadataMutation(context.Background(), "kms/keys/tag", make(chan int)); err == nil {
		t.Fatal("expected a marshal error")
	}
}

func TestListKmsKeysCapabilityError(t *testing.T) {
	client := newKmsTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"not implemented"}`))
	})

	if _, err := client.ListKmsKeys(context.Background()); !IsUnsupportedFeature(err) {
		t.Fatalf("expected an unsupported-feature error, got %v", err)
	}
}
