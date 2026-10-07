package rustfs

import (
	"encoding/json"
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

	status, err := client.GetKmsStatus()
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

	status, err := client.GetKmsStatus()
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

	config, err := client.GetKmsConfig()
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

	config, err := client.GetKmsConfig()
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

	if _, err := client.GetKmsStatus(); err == nil {
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

	if _, err := client.GetKmsConfig(); err == nil {
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

	key, err := client.CreateKmsKey("mykey")
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

	key, err := client.DescribeKmsKey("k1")
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

	keys, err := client.ListKmsKeys()
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

	if err := client.EnableKmsKey("mykey"); err != nil {
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

	if err := client.DisableKmsKey("mykey"); err != nil {
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

	key, err := client.RotateKmsKey("mykey")
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

	if err := client.DeleteKmsKey("mykey"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
