package rustfs

import (
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
