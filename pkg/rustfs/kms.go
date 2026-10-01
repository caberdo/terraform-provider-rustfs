package rustfs

import (
	"context"
	"encoding/json"
)

// KmsCacheStats holds KMS key cache counters reported by /kms/status.
type KmsCacheStats struct {
	HitCount      uint64 `json:"hit_count"`
	MissCount     uint64 `json:"miss_count"`
	EntryCount    uint64 `json:"entry_count"`
	EvictionCount uint64 `json:"eviction_count"`
}

// KmsClusterNode describes a single node's KMS configuration state.
type KmsClusterNode struct {
	Host              string  `json:"host"`
	ConfigFingerprint *string `json:"config_fingerprint"`
	Error             *string `json:"error"`
}

// KmsClusterConfig describes cluster-wide KMS configuration consistency.
type KmsClusterConfig struct {
	Consistent bool             `json:"consistent"`
	Nodes      []KmsClusterNode `json:"nodes"`
}

// KmsStatus mirrors the GET /kms/status response body.
type KmsStatus struct {
	BackendType   string            `json:"backend_type"`
	BackendStatus string            `json:"backend_status"`
	CacheEnabled  bool              `json:"cache_enabled"`
	CacheStats    *KmsCacheStats    `json:"cache_stats"`
	DefaultKeyID  *string           `json:"default_key_id"`
	Capabilities  map[string]bool   `json:"capabilities"`
	ClusterConfig *KmsClusterConfig `json:"cluster_config"`
}

// KmsConfig mirrors the GET /kms/config response body.
type KmsConfig struct {
	Backend         string  `json:"backend"`
	CacheEnabled    bool    `json:"cache_enabled"`
	CacheMaxKeys    int     `json:"cache_max_keys"`
	CacheTTLSeconds int     `json:"cache_ttl_seconds"`
	DefaultKeyID    *string `json:"default_key_id"`
}

// GetKmsStatus returns the current KMS service status.
func (c *RustfsAdmin) GetKmsStatus() (*KmsStatus, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "kms/status",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var status KmsStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}
	return &status, nil
}

// GetKmsConfig returns the current KMS backend configuration.
func (c *RustfsAdmin) GetKmsConfig() (*KmsConfig, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "kms/config",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var config KmsConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, err
	}
	return &config, nil
}
