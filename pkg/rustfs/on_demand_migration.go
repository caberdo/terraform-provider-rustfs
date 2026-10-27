// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// FeatureOnDemandMigration is the per-bucket lazy migration configuration
// (PUT|GET|DELETE /rustfs/admin/v3/on-demand-migration/{bucket}) and its
// per-node status route. The routes exist in every supported release (1.0.1+).
const FeatureOnDemandMigration Feature = "bucket on-demand migration"

// RedactedSecret is the placeholder RustFS substitutes for every persisted
// credential in a configuration read.
const RedactedSecret = "REDACTED"

// ODMConfig is the persisted on-demand migration configuration.
//
// The server denies unknown fields (`deny_unknown_fields`) and replaces
// missing fields with the documented defaults, so every field is sent
// explicitly. Version must stay 1.
type ODMConfig struct {
	Version int       `json:"version"`
	Enabled bool      `json:"enabled"`
	Source  ODMSource `json:"source"`
	Filter  ODMFilter `json:"filter"`
	Policy  ODMPolicy `json:"policy"`
}

// ODMSource is the external S3-compatible source bucket.
type ODMSource struct {
	Provider string `json:"provider"`
	// Endpoint is optional only for aws (derived from region), azure (derived
	// from the account) and gcs_native (fixed Google endpoint).
	Endpoint    *string         `json:"endpoint"`
	Region      string          `json:"region"`
	Bucket      string          `json:"bucket"`
	PathStyle   string          `json:"path_style"`
	Credentials *ODMCredentials `json:"credentials"`
	TLS         ODMTLS          `json:"tls"`
	// Azure is required for provider azure and rejected for every other
	// provider.
	Azure *ODMAzure `json:"azure,omitempty"`
	// GCS is required for provider gcs_native and rejected for every other
	// provider.
	GCS *ODMGCS `json:"gcs,omitempty"`
}

// ODMCredentials are the static source credentials. Anonymous sources are not
// supported by the server.
type ODMCredentials struct {
	AccessKey    string  `json:"access_key"`
	SecretKey    string  `json:"secret_key"`
	SessionToken *string `json:"session_token"`
}

// ODMTLS configures the outbound source connection.
type ODMTLS struct {
	SkipVerify bool    `json:"skip_verify"`
	CACertPEM  *string `json:"ca_cert_pem"`
}

// ODMAzure carries native Azure Blob source parameters. Exactly one of
// AccountKey and SASToken is set.
type ODMAzure struct {
	Account    string  `json:"account"`
	AccountKey *string `json:"account_key"`
	SASToken   *string `json:"sas_token"`
}

// ODMGCS carries native Google Cloud Storage source parameters.
type ODMGCS struct {
	ServiceAccountJSON string `json:"service_account_json"`
}

// ODMFilter selects which local keys consult the source.
type ODMFilter struct {
	Prefix       *string `json:"prefix"`
	SourcePrefix *string `json:"source_prefix"`
}

// ODMPolicy is the documented read-path policy. Bounds are enforced by the
// resource schema before a request is sent.
type ODMPolicy struct {
	Head                      string           `json:"head"`
	RangeGet                  string           `json:"range_get"`
	SourceError               string           `json:"source_error"`
	ListThrough               bool             `json:"list_through"`
	RespectLocalDeleteMarker  bool             `json:"respect_local_delete_marker"`
	PreserveETag              bool             `json:"preserve_etag"`
	CopyTags                  bool             `json:"copy_tags"`
	EmitEvents                bool             `json:"emit_events"`
	NegativeCacheTTLSecs      uint64           `json:"negative_cache_ttl_secs"`
	InlineMaxBytes            uint64           `json:"inline_max_bytes"`
	MultipartPartSizeBytes    uint64           `json:"multipart_part_size_bytes"`
	MaxConcurrentPulls        uint32           `json:"max_concurrent_pulls"`
	PullQueueCapacity         uint32           `json:"pull_queue_capacity"`
	SourceTimeout             ODMSourceTimeout `json:"source_timeout"`
	BandwidthLimitBytesPerSec *uint64          `json:"bandwidth_limit_bytes_per_sec"`
}

// ODMSourceTimeout is the outbound source timeout budget in milliseconds.
type ODMSourceTimeout struct {
	ConnectMS   uint64 `json:"connect_ms"`
	FirstByteMS uint64 `json:"first_byte_ms"`
	IdleMS      uint64 `json:"idle_ms"`
}

// ODMSetResult is the answer to a set request. UpdatedAt is nil for a dry run.
type ODMSetResult struct {
	Bucket    string          `json:"bucket"`
	DryRun    bool            `json:"dry_run"`
	Config    ODMConfig       `json:"config"`
	UpdatedAt *string         `json:"updated_at"`
	Probe     ODMProbeSummary `json:"probe"`
}

// ODMProbeSummary reports what the source answered during validation.
type ODMProbeSummary struct {
	Reachable bool    `json:"reachable"`
	Listable  bool    `json:"listable"`
	SampleKey *string `json:"sample_key"`
}

// ODMGetResult is the answer to a configuration read. The credentials are
// redacted.
type ODMGetResult struct {
	Bucket    string    `json:"bucket"`
	Config    ODMConfig `json:"config"`
	UpdatedAt string    `json:"updated_at"`
}

// ODMStatus is the per-node runtime snapshot returned by the status route.
type ODMStatus struct {
	Configured          bool                `json:"configured"`
	Enabled             bool                `json:"enabled"`
	ModuleEnabled       bool                `json:"module_enabled"`
	Provider            *string             `json:"provider"`
	EndpointHost        *string             `json:"endpoint_host"`
	Breaker             *ODMBreakerStatus   `json:"breaker"`
	Counters            *ODMCounters        `json:"counters"`
	LastSourceError     *ODMLastSourceError `json:"last_source_error"`
	InflightPulls       uint64              `json:"inflight_pulls"`
	QueueDepth          uint64              `json:"queue_depth"`
	ServedBySourceRatio *float64            `json:"served_by_source_ratio"`
	UpdatedAt           *string             `json:"updated_at"`
	Backfill            *ODMBackfillSummary `json:"backfill,omitempty"`
}

// ODMBreakerStatus is the per-node circuit breaker state.
type ODMBreakerStatus struct {
	State    string  `json:"state"`
	OpenedAt *string `json:"opened_at"`
}

// ODMCounters are the lifetime counters of this node's bucket runtime.
type ODMCounters struct {
	RequestsTotal      map[string]map[string]uint64 `json:"requests_total"`
	PulledBytesTotal   uint64                       `json:"pulled_bytes_total"`
	PulledObjectsTotal map[string]uint64            `json:"pulled_objects_total"`
	PullFailuresTotal  map[string]uint64            `json:"pull_failures_total"`
	SourceLatency      ODMSourceLatency             `json:"source_latency"`
}

// ODMSourceLatency is a histogram snapshot of source response time.
type ODMSourceLatency struct {
	Buckets []ODMLatencyBucket `json:"buckets"`
	Count   uint64             `json:"count"`
	SumMS   uint64             `json:"sum_ms"`
}

// ODMLatencyBucket is one cumulative histogram bucket.
type ODMLatencyBucket struct {
	LeMS  uint64 `json:"le_ms"`
	Count uint64 `json:"count"`
}

// ODMLastSourceError is the last classified source failure seen on this node.
type ODMLastSourceError struct {
	Class string `json:"class"`
	At    string `json:"at"`
}

// ODMBackfillSummary is the latest backfill job of the bucket.
type ODMBackfillSummary struct {
	JobID           string `json:"job_id"`
	State           string `json:"state"`
	Listed          uint64 `json:"listed"`
	Enqueued        uint64 `json:"enqueued"`
	Pulled          uint64 `json:"pulled"`
	SkippedExisting uint64 `json:"skipped_existing"`
	Failed          uint64 `json:"failed"`
	Bytes           uint64 `json:"bytes"`
	UpdatedAt       string `json:"updated_at"`
}

// SetBucketOnDemandMigration validates and probes the source, then persists the
// configuration (PUT /rustfs/admin/v3/on-demand-migration/{bucket}). With
// dryRun the server validates and probes but writes nothing.
func (c *RustfsAdmin) SetBucketOnDemandMigration(ctx context.Context, bucket string, config ODMConfig, dryRun bool) (ODMSetResult, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return ODMSetResult{}, err
	}

	reqData := RequestData{
		Method:  http.MethodPut,
		RelPath: "on-demand-migration/" + url.PathEscape(bucket),
		Content: body,
	}
	if dryRun {
		reqData.QueryValues = url.Values{"dry-run": []string{"true"}}
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return ODMSetResult{}, c.odmError(ctx, err)
	}
	defer drainClose(resp)

	var result ODMSetResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ODMSetResult{}, err
	}
	return result, nil
}

// GetBucketOnDemandMigration reads the saved configuration. The server answers
// 404 NoSuchConfiguration when the bucket has none; the returned *APIError
// carries that code.
func (c *RustfsAdmin) GetBucketOnDemandMigration(ctx context.Context, bucket string) (ODMGetResult, error) {
	reqData := RequestData{
		Method:  http.MethodGet,
		RelPath: "on-demand-migration/" + url.PathEscape(bucket),
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return ODMGetResult{}, c.odmError(ctx, err)
	}
	defer drainClose(resp)

	var result ODMGetResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ODMGetResult{}, err
	}
	return result, nil
}

// DeleteBucketOnDemandMigration clears the configuration. The route is
// idempotent and answers 204 even when nothing was configured; already-pulled
// objects stay in place.
func (c *RustfsAdmin) DeleteBucketOnDemandMigration(ctx context.Context, bucket string) error {
	reqData := RequestData{
		Method:  http.MethodDelete,
		RelPath: "on-demand-migration/" + url.PathEscape(bucket),
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return c.odmError(ctx, err)
	}
	drainClose(resp)
	return nil
}

// GetBucketOnDemandMigrationStatus reads the per-node runtime snapshot. In a
// distributed deployment it describes only the node that answered.
func (c *RustfsAdmin) GetBucketOnDemandMigrationStatus(ctx context.Context, bucket string) (ODMStatus, error) {
	reqData := RequestData{
		Method:  http.MethodGet,
		RelPath: "on-demand-migration/" + url.PathEscape(bucket) + "/status",
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return ODMStatus{}, c.odmError(ctx, err)
	}
	defer drainClose(resp)

	var status ODMStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return ODMStatus{}, err
	}
	return status, nil
}

// Redacted returns a copy of the configuration with every secret replaced by
// RedactedSecret, mirroring what the server returns from a read.
func (config ODMConfig) Redacted() ODMConfig {
	copy := config
	if copy.Source.Credentials != nil {
		credentials := *copy.Source.Credentials
		credentials.SecretKey = RedactedSecret
		if credentials.SessionToken != nil {
			redacted := RedactedSecret
			credentials.SessionToken = &redacted
		}
		copy.Source.Credentials = &credentials
	}
	if copy.Source.Azure != nil {
		azure := *copy.Source.Azure
		if azure.AccountKey != nil {
			redacted := RedactedSecret
			azure.AccountKey = &redacted
		}
		if azure.SASToken != nil {
			redacted := RedactedSecret
			azure.SASToken = &redacted
		}
		copy.Source.Azure = &azure
	}
	if copy.Source.GCS != nil {
		gcs := *copy.Source.GCS
		gcs.ServiceAccountJSON = RedactedSecret
		copy.Source.GCS = &gcs
	}
	return copy
}

// odmError keeps the route's semantic error codes (NoSuchConfiguration,
// NoSuchBucket, OnDemandMigrationDisabled, OnDemandMigrationBackendNotCompiled,
// ...) intact, and only turns a bare 404/405/501 into the server-capability
// diagnostic. featureError alone would mistake NoSuchConfiguration for a
// missing route.
func (c *RustfsAdmin) odmError(ctx context.Context, err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Code != "" && !strings.EqualFold(apiErr.Code, "NotImplemented") {
			return err
		}
	}
	return c.featureError(ctx, err, FeatureOnDemandMigration)
}

func init() {
	registerFeature(FeatureOnDemandMigration, featureSpec{
		route: "PUT|GET|DELETE /rustfs/admin/v3/on-demand-migration/{bucket}",
		// The ODM admin handlers are present, and wired into the admin router,
		// in every supported release (1.0.1 through 1.0.3). A 1.0.1 server is
		// therefore the lowest version that can serve this feature.
		minVersion: &Version{Major: 1, Minor: 0, Patch: 1, Raw: "1.0.1"},
	})
}
