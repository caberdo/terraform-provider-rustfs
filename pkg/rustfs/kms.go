// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// FeatureKmsKeyList is the KMS key listing read by the rustfs_kms_keys data
// source (GET /rustfs/admin/v3/kms/keys).
const FeatureKmsKeyList Feature = "KMS key listing"

func init() {
	registerFeature(FeatureKmsKeyList, featureSpec{
		route: "GET /rustfs/admin/v3/kms/keys",
	})
}

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
func (c *RustfsAdmin) GetKmsStatus(ctx context.Context) (*KmsStatus, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "kms/status",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	var status KmsStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}
	return &status, nil
}

// GetKmsConfig returns the current KMS backend configuration.
func (c *RustfsAdmin) GetKmsConfig(ctx context.Context) (*KmsConfig, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "kms/config",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	var config KmsConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, err
	}
	return &config, nil
}

// KmsKey describes a KMS master key as returned by the KMS admin API.
//
// The wire shape is the key_metadata object of the create/describe/lifecycle
// responses. `key_state` is the authoritative lifecycle state (Enabled,
// Disabled or PendingDeletion).
type KmsKey struct {
	KeyID        string            `json:"key_id"`
	KeyState     string            `json:"key_state"`
	KeyUsage     string            `json:"key_usage"`
	Description  *string           `json:"description"`
	CreationDate string            `json:"creation_date"`
	DeletionDate *string           `json:"deletion_date"`
	Origin       string            `json:"origin"`
	KeyManager   string            `json:"key_manager"`
	Tags         map[string]string `json:"tags"`
}

// KmsKeyInfo is a single entry in a key listing response.
type KmsKeyInfo struct {
	KeyID       string            `json:"key_id"`
	Description *string           `json:"description"`
	Algorithm   string            `json:"algorithm"`
	Usage       string            `json:"usage"`
	Status      string            `json:"status"`
	Tags        map[string]string `json:"tags"`
	CreatedAt   string            `json:"created_at"`
	CreatedBy   *string           `json:"created_by"`
}

type kmsCreateResponse struct {
	Success     bool    `json:"success"`
	Message     string  `json:"message"`
	KeyID       string  `json:"key_id"`
	KeyMetadata *KmsKey `json:"key_metadata"`
}

type kmsDescribeResponse struct {
	Success     bool    `json:"success"`
	Message     string  `json:"message"`
	KeyMetadata *KmsKey `json:"key_metadata"`
}

type kmsListResponse struct {
	Success    bool         `json:"success"`
	Message    string       `json:"message"`
	Keys       []KmsKeyInfo `json:"keys"`
	Truncated  bool         `json:"truncated"`
	NextMarker *string      `json:"next_marker"`
}

type kmsLifecycleResponse struct {
	Success     bool    `json:"success"`
	Message     string  `json:"message"`
	KeyID       string  `json:"key_id"`
	KeyMetadata *KmsKey `json:"key_metadata"`
}

// CreateKmsKey creates a new KMS master key with the given name.
//
// The key name is carried as the `name` tag, which the RustFS create handler
// turns into the key id for backends that support named keys (Local backend)
// or into a server-generated key id for production backends.
func (c *RustfsAdmin) CreateKmsKey(ctx context.Context, name string) (KmsKey, error) {
	body := struct {
		Tags map[string]string `json:"tags"`
	}{Tags: map[string]string{"name": name}}
	//#nosec G117 — a KMS key name is a public identifier, not a secret
	bytes, err := json.Marshal(body)
	if err != nil {
		return KmsKey{}, err
	}
	reqData := RequestData{
		Method:  "POST",
		RelPath: "kms/keys",
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return KmsKey{}, err
	}
	defer drainClose(resp)
	var out kmsCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return KmsKey{}, err
	}
	if out.KeyMetadata != nil {
		return *out.KeyMetadata, nil
	}
	return KmsKey{KeyID: out.KeyID, KeyState: "Enabled", Tags: body.Tags}, nil
}

// DescribeKmsKey describes a KMS master key by its key id.
func (c *RustfsAdmin) DescribeKmsKey(ctx context.Context, keyID string) (KmsKey, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "kms/keys/" + keyID,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return KmsKey{}, err
	}
	defer drainClose(resp)
	var out kmsDescribeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return KmsKey{}, err
	}
	if out.KeyMetadata == nil {
		return KmsKey{}, errors.New(out.Message)
	}
	return *out.KeyMetadata, nil
}

// ListKmsKeys lists the KMS master keys known to the server.
func (c *RustfsAdmin) ListKmsKeys(ctx context.Context) ([]KmsKeyInfo, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "kms/keys",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureErrorPreservingCode(ctx, err, FeatureKmsKeyList)
	}
	defer drainClose(resp)
	var out kmsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Keys, nil
}

// EnableKmsKey enables a KMS master key.
func (c *RustfsAdmin) EnableKmsKey(ctx context.Context, keyID string) error {
	return c.setKmsKeyState(ctx, keyID, "kms/keys/enable")
}

// DisableKmsKey disables a KMS master key.
func (c *RustfsAdmin) DisableKmsKey(ctx context.Context, keyID string) error {
	return c.setKmsKeyState(ctx, keyID, "kms/keys/disable")
}

func (c *RustfsAdmin) setKmsKeyState(ctx context.Context, keyID, relPath string) error {
	body := struct {
		KeyID string `json:"key_id"`
	}{KeyID: keyID}
	//#nosec G117 — a KMS key id is a public identifier, not a secret
	bytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	reqData := RequestData{
		Method:  "POST",
		RelPath: relPath,
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	var out kmsLifecycleResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if !out.Success {
		return errors.New(out.Message)
	}
	return nil
}

// ErrKmsRekeyNotRun reports that the server has no current or last rekey
// sweep. RustFS keeps the sweep record in memory, so a server restart loses it.
var ErrKmsRekeyNotRun = errors.New("no rekey sweep has run")

// KmsRekeyStatus mirrors the snapshot returned by the rekey start, status and
// cancel routes. State is one of "running", "completed" or "cancelled".
type KmsRekeyStatus struct {
	JobID          string   `json:"job_id"`
	State          string   `json:"state"`
	Buckets        []string `json:"buckets"`
	CurrentBucket  *string  `json:"current_bucket"`
	Scanned        uint64   `json:"scanned"`
	Rewrapped      uint64   `json:"rewrapped"`
	AlreadyCurrent uint64   `json:"already_current"`
	NotApplicable  uint64   `json:"not_applicable"`
	Failed         uint64   `json:"failed"`
}

// StartKmsRekey starts a bulk data-key-envelope rewrap sweep. An empty buckets
// slice sweeps every bucket; an empty prefix covers the whole bucket.
func (c *RustfsAdmin) StartKmsRekey(ctx context.Context, buckets []string, prefix string) (KmsRekeyStatus, error) {
	body := struct {
		Buckets []string `json:"buckets,omitempty"`
		Prefix  string   `json:"prefix,omitempty"`
	}{Buckets: buckets, Prefix: prefix}
	return c.kmsRekeyCall(ctx, http.MethodPost, "kms/keys/rekey", body)
}

// GetKmsRekeyStatus returns the current or most recent sweep.
func (c *RustfsAdmin) GetKmsRekeyStatus(ctx context.Context) (KmsRekeyStatus, error) {
	return c.kmsRekeyCall(ctx, http.MethodGet, "kms/keys/rekey/status", nil)
}

// CancelKmsRekey asks the running sweep to stop. Cancellation is cooperative:
// the returned snapshot may still report "running" until the current object
// finishes.
func (c *RustfsAdmin) CancelKmsRekey(ctx context.Context) (KmsRekeyStatus, error) {
	return c.kmsRekeyCall(ctx, http.MethodPost, "kms/keys/rekey/cancel", nil)
}

func (c *RustfsAdmin) kmsRekeyCall(ctx context.Context, method, relPath string, body any) (KmsRekeyStatus, error) {
	var content []byte
	if body != nil {
		//#nosec G117 — the sweep scope is bucket names and a prefix, not a secret
		bytes, err := json.Marshal(body)
		if err != nil {
			return KmsRekeyStatus{}, err
		}
		content = bytes
	}
	reqData := RequestData{
		Method:  method,
		RelPath: relPath,
		Content: content,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == http.StatusNotFound && strings.Contains(apiErr.Raw, "no rekey sweep has run") {
				return KmsRekeyStatus{}, ErrKmsRekeyNotRun
			}
			if apiErr.StatusCode == http.StatusConflict {
				return KmsRekeyStatus{}, fmt.Errorf("a rekey sweep is already running: %w", err)
			}
		}
		return KmsRekeyStatus{}, kmsCapabilityError(err, "rewrapping data-key envelopes")
	}
	defer drainClose(resp)
	var out KmsRekeyStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return KmsRekeyStatus{}, err
	}
	return out, nil
}

// KmsBackupStatus mirrors GET /kms/backup. It reports readiness and
// identifiers only; it never carries key material.
type KmsBackupStatus struct {
	ServiceStatus       string  `json:"service_status"`
	Backend             *string `json:"backend"`
	Responsibility      *string `json:"responsibility"`
	ExportSupported     bool    `json:"export_supported"`
	DeploymentIdentity  *string `json:"deployment_identity"`
	BackupRoot          *string `json:"backup_root"`
	BackupKEKConfigured bool    `json:"backup_kek_configured"`
	BackupKEKID         *string `json:"backup_kek_id"`
	BackupKEKVersion    *int64  `json:"backup_kek_version"`
	TargetKeyDir        *string `json:"target_key_dir"`
	UnavailableReason   *string `json:"unavailable_reason"`
}

// KmsBackupBundle mirrors the POST /kms/backup response: identifiers and
// metadata for one bundle. The route never returns key material.
type KmsBackupBundle struct {
	BackupID                string `json:"backup_id"`
	SnapshotGeneration      uint64 `json:"snapshot_generation"`
	DeploymentIdentity      string `json:"deployment_identity"`
	ArtifactCount           int    `json:"artifact_count"`
	ManifestDigest          string `json:"manifest_digest"`
	BackupKEKID             string `json:"backup_kek_id"`
	BackupKEKVersion        uint32 `json:"backup_kek_version"`
	SanitizedConfigIncluded bool   `json:"sanitized_config_included"`
}

// GetKmsBackupStatus returns whether this server can export a bundle.
func (c *RustfsAdmin) GetKmsBackupStatus(ctx context.Context) (KmsBackupStatus, error) {
	reqData := RequestData{
		Method:  http.MethodGet,
		RelPath: "kms/backup",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return KmsBackupStatus{}, err
	}
	defer drainClose(resp)
	var out KmsBackupStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return KmsBackupStatus{}, err
	}
	return out, nil
}

// CreateKmsBackup writes one bundle under the server's configured backup root
// and returns its metadata. backupID must be a single validated name.
func (c *RustfsAdmin) CreateKmsBackup(ctx context.Context, backupID string) (KmsBackupBundle, error) {
	body := struct {
		BackupID string `json:"backup_id,omitempty"`
	}{BackupID: backupID}
	//#nosec G117 — a bundle name is a public identifier, not a secret
	bytes, err := json.Marshal(body)
	if err != nil {
		return KmsBackupBundle{}, err
	}
	reqData := RequestData{
		Method:  http.MethodPost,
		RelPath: "kms/backup",
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return KmsBackupBundle{}, kmsCapabilityError(err, "KMS backup")
	}
	defer drainClose(resp)
	var out KmsBackupBundle
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return KmsBackupBundle{}, err
	}
	return out, nil
}

// UpdateKmsKeyDescription replaces the description stored on a KMS master key.
// An empty description clears it.
func (c *RustfsAdmin) UpdateKmsKeyDescription(ctx context.Context, keyID, description string) (KmsKey, error) {
	body := struct {
		KeyID       string `json:"key_id"`
		Description string `json:"description"`
	}{KeyID: keyID, Description: description}
	return c.kmsKeyMetadataMutation(ctx, "kms/keys/update-description", body)
}

// TagKmsKey adds or overwrites the given tags on a KMS master key. Tags outside
// the map are left untouched.
func (c *RustfsAdmin) TagKmsKey(ctx context.Context, keyID string, tags map[string]string) (KmsKey, error) {
	body := struct {
		KeyID string            `json:"key_id"`
		Tags  map[string]string `json:"tags"`
	}{KeyID: keyID, Tags: tags}
	return c.kmsKeyMetadataMutation(ctx, "kms/keys/tag", body)
}

// UntagKmsKey removes the named tags from a KMS master key. Tag keys that are
// not set are ignored by the server.
func (c *RustfsAdmin) UntagKmsKey(ctx context.Context, keyID string, tagKeys []string) (KmsKey, error) {
	body := struct {
		KeyID   string   `json:"key_id"`
		TagKeys []string `json:"tag_keys"`
	}{KeyID: keyID, TagKeys: tagKeys}
	return c.kmsKeyMetadataMutation(ctx, "kms/keys/untag", body)
}

func (c *RustfsAdmin) kmsKeyMetadataMutation(ctx context.Context, relPath string, body any) (KmsKey, error) {
	//#nosec G117 — key ids, descriptions and tags are public metadata, not secrets
	bytes, err := json.Marshal(body)
	if err != nil {
		return KmsKey{}, err
	}
	reqData := RequestData{
		Method:  "POST",
		RelPath: relPath,
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return KmsKey{}, err
	}
	defer drainClose(resp)
	var out kmsLifecycleResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return KmsKey{}, err
	}
	if out.KeyMetadata == nil {
		return KmsKey{}, errors.New(out.Message)
	}
	return *out.KeyMetadata, nil
}

// kmsCapabilityError explains a 501 from a KMS route as a limitation of the
// configured backend rather than a missing server route. RustFS answers 501
// for operations the backend cannot perform (rotation on Local, rekey on
// Local/Static/AWS, backup on non-Local), so the route exists and the generic
// "server does not implement" wording would be wrong.
func kmsCapabilityError(err error, capability string) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotImplemented {
		return fmt.Errorf("the configured KMS backend does not support %s: %w", capability, err)
	}
	return err
}

// RotateKmsKey rotates a KMS master key and returns the key's new metadata.
//
// Note that the Local development backend does not support rotation: the
// server answers with a 501 (KmsError::UnsupportedCapability) for backends
// whose capabilities report rotate=false.
func (c *RustfsAdmin) RotateKmsKey(ctx context.Context, keyID string) (KmsKey, error) {
	body := struct {
		KeyID string `json:"key_id"`
	}{KeyID: keyID}
	//#nosec G117 — a KMS key id is a public identifier, not a secret
	bytes, err := json.Marshal(body)
	if err != nil {
		return KmsKey{}, err
	}
	reqData := RequestData{
		Method:  "POST",
		RelPath: "kms/keys/rotate",
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return KmsKey{}, kmsCapabilityError(err, "key rotation")
	}
	defer drainClose(resp)
	var out kmsLifecycleResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return KmsKey{}, err
	}
	if out.KeyMetadata == nil {
		return KmsKey{}, errors.New(out.Message)
	}
	return *out.KeyMetadata, nil
}

// DeleteKmsKey schedules the deletion of a KMS master key.
//
// The RustFS admin API marks this route Critical: destroying a master key
// makes every object encrypted under it permanently unreadable. The server
// schedules the deletion behind a pending window (default 30 days) unless
// force_immediate/confirm_key_id are supplied; this client deliberately never
// sends them, so callers always get the cancellable scheduled deletion.
func (c *RustfsAdmin) DeleteKmsKey(ctx context.Context, keyID string) error {
	return c.DeleteKmsKeyWithWindow(ctx, keyID, nil)
}

// DeleteKmsKeyWithWindow schedules deletion and asks the server to wait
// pendingWindowDays (7 to 30) before destroying the key material. A nil window
// leaves the server default (30 days) in place.
func (c *RustfsAdmin) DeleteKmsKeyWithWindow(ctx context.Context, keyID string, pendingWindowDays *int64) error {
	body := struct {
		KeyID               string `json:"key_id"`
		PendingWindowInDays *int64 `json:"pending_window_in_days,omitempty"`
	}{KeyID: keyID, PendingWindowInDays: pendingWindowDays}
	//#nosec G117 — a KMS key id is a public identifier, not a secret
	bytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	reqData := RequestData{
		Method:  "DELETE",
		RelPath: "kms/keys/delete",
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}
