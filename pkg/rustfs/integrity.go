// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The data-integrity routes below are registered and implemented in the
// tagged RustFS source for 1.0.1 through 1.0.3, so no feature carries a
// version floor. A build that does not implement them produces a capability
// error naming the exact server version.
const (
	// FeatureIntegrityReadiness is GET /rustfs/admin/v3/integrity/readiness.
	FeatureIntegrityReadiness Feature = "data-integrity readiness"

	// FeatureIntegrityInventory is GET /rustfs/admin/v3/integrity/{bucket}/inventory.
	FeatureIntegrityInventory Feature = "data-integrity inventory"

	// FeatureIntegrityJobs is the job create, status and control surface under
	// /rustfs/admin/v3/integrity/{bucket}/jobs.
	FeatureIntegrityJobs Feature = "data-integrity jobs"
)

// Job modes accepted by POST /rustfs/admin/v3/integrity/{bucket}/jobs.
const (
	IntegrityModeAudit   = "audit"
	IntegrityModeMigrate = "migrate"
)

// Control operations accepted by POST /rustfs/admin/v3/integrity/{bucket}/jobs/{job_id}/control.
const (
	IntegrityControlPause  = "pause"
	IntegrityControlResume = "resume"
	IntegrityControlCancel = "cancel"
)

// IntegrityReadiness is the deployment's shard-integrity readiness from
// GET /rustfs/admin/v3/integrity/readiness.
type IntegrityReadiness struct {
	LocalReaderSupported    bool     `json:"local_reader_supported"`
	WriteRequested          bool     `json:"write_requested"`
	FleetOperatorAttested   bool     `json:"fleet_operator_attested"`
	NewWritesEnabledHere    bool     `json:"new_writes_enabled_here"`
	FleetCapabilityVerified bool     `json:"fleet_capability_verified"`
	UnverifiedRequirements  []string `json:"unverified_requirements"`
}

// IntegrityItemRequest is one explicit source version in a job request.
//
// VersionID is the S3 version selector; the literal "null" selects the null
// version slot. TargetKey is only valid for mode "migrate", where it must
// differ from every source and every other target.
type IntegrityItemRequest struct {
	Key            string  `json:"key"`
	VersionID      *string `json:"version_id"`
	ExpectedSHA256 *string `json:"expected_sha256"`
	TargetKey      *string `json:"target_key"`
}

// IntegrityJobRequest is the body of POST /rustfs/admin/v3/integrity/{bucket}/jobs.
//
// A job covers 1..=64 explicit versions. BytesPerSecond bounds each source
// read, destination PUT and verification stream; MaxObjectBytes bounds the
// staging limit. One integrity worker runs per cluster.
type IntegrityJobRequest struct {
	Mode           string                 `json:"mode"`
	Items          []IntegrityItemRequest `json:"items"`
	BytesPerSecond int64                  `json:"bytes_per_second"`
	MaxObjectBytes int64                  `json:"max_object_bytes"`
}

// IntegrityItemResult is the per-item outcome recorded in a job.
type IntegrityItemResult struct {
	State             string  `json:"state"`
	SourceFingerprint *string `json:"source_fingerprint"`
	ExpectedSHA256    *string `json:"expected_sha256"`
	EvidenceSource    *string `json:"evidence_source"`
	SourceSize        *int64  `json:"source_size"`
	TargetVersionID   *string `json:"target_version_id"`
	Detail            *string `json:"detail"`
}

// IntegrityJob is the durable job record returned by the create, status and
// control routes.
type IntegrityJob struct {
	FormatVersion     int64                 `json:"format_version"`
	ID                string                `json:"id"`
	Bucket            string                `json:"bucket"`
	BucketIncarnation string                `json:"bucket_incarnation"`
	Request           IntegrityJobRequest   `json:"request"`
	State             string                `json:"state"`
	Revision          int64                 `json:"revision"`
	Results           []IntegrityItemResult `json:"results"`
	LastError         *string               `json:"last_error"`
}

// IntegrityInventoryItem is one object version in an inventory page.
type IntegrityInventoryItem struct {
	Key                 string            `json:"key"`
	VersionID           string            `json:"version_id"`
	DataDir             *string           `json:"data_dir"`
	Size                int64             `json:"size"`
	Protection          string            `json:"protection"`
	Checksums           map[string]string `json:"checksums"`
	ChecksumIsMultipart bool              `json:"checksum_is_multipart"`
	AuditUnsupported    *string           `json:"audit_unsupported"`
	SourceFingerprint   string            `json:"source_fingerprint"`
	ObservationError    *string           `json:"observation_error"`
}

// IntegrityInventoryPage is one page of GET /rustfs/admin/v3/integrity/{bucket}/inventory.
type IntegrityInventoryPage struct {
	ObservedAt        string                   `json:"observed_at"`
	BucketIncarnation string                   `json:"bucket_incarnation"`
	Items             []IntegrityInventoryItem `json:"items"`
	IsTruncated       bool                     `json:"is_truncated"`
	NextKeyMarker     *string                  `json:"next_key_marker"`
	NextVersionMarker *string                  `json:"next_version_marker"`
}

// IntegrityReadiness reads GET /rustfs/admin/v3/integrity/readiness.
func (c *RustfsAdmin) IntegrityReadiness(ctx context.Context) (IntegrityReadiness, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "integrity/readiness",
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		// Readiness is not bucket-scoped, so a 404 means the route was never
		// registered and the generic capability conversion is safe here.
		return IntegrityReadiness{}, c.featureError(ctx, err, FeatureIntegrityReadiness)
	}
	defer drainClose(resp)

	var readiness IntegrityReadiness
	if err := json.NewDecoder(resp.Body).Decode(&readiness); err != nil {
		return IntegrityReadiness{}, err
	}
	return readiness, nil
}

// IntegrityInventory reads one inventory page for a bucket. Empty strings omit
// the corresponding query parameter; a limit of zero leaves the server default
// (100) in place.
func (c *RustfsAdmin) IntegrityInventory(ctx context.Context, bucket, prefix, keyMarker, versionMarker string, limit int) (IntegrityInventoryPage, error) {
	query := url.Values{}
	if prefix != "" {
		query.Set("prefix", prefix)
	}
	if keyMarker != "" {
		query.Set("key-marker", keyMarker)
	}
	if versionMarker != "" {
		query.Set("version-marker", versionMarker)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}

	reqData := RequestData{
		Method:      "GET",
		RelPath:     "integrity/" + url.PathEscape(bucket) + "/inventory",
		QueryValues: query,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return IntegrityInventoryPage{}, c.integrityFeatureError(ctx, err, FeatureIntegrityInventory)
	}
	defer drainClose(resp)

	var page IntegrityInventoryPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return IntegrityInventoryPage{}, err
	}
	return page, nil
}

// CreateIntegrityJob creates a durable job. The server answers 201 with the
// job in state "paused": nothing runs until ControlIntegrityJob resumes it.
func (c *RustfsAdmin) CreateIntegrityJob(ctx context.Context, bucket string, request IntegrityJobRequest) (IntegrityJob, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return IntegrityJob{}, err
	}

	reqData := RequestData{
		Method:  "POST",
		RelPath: "integrity/" + url.PathEscape(bucket) + "/jobs",
		Content: body,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return IntegrityJob{}, c.integrityFeatureError(ctx, err, FeatureIntegrityJobs)
	}
	defer drainClose(resp)

	var job IntegrityJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return IntegrityJob{}, err
	}
	return job, nil
}

// IntegrityJob reads one durable job. The server reports a job it no longer
// holds as NoSuchKey, which callers detect with IsAPIErrorCode.
func (c *RustfsAdmin) IntegrityJob(ctx context.Context, bucket, jobID string) (IntegrityJob, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "integrity/" + url.PathEscape(bucket) + "/jobs/" + url.PathEscape(jobID),
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		// A missing job is NoSuchKey on an implemented route, so this method
		// deliberately does not convert a 404 into a capability gap.
		return IntegrityJob{}, err
	}
	defer drainClose(resp)

	var job IntegrityJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return IntegrityJob{}, err
	}
	return job, nil
}

// ControlIntegrityJob sends pause, resume or cancel to a durable job. Pause and
// cancel acknowledge intent; an in-flight conditional publication is allowed to
// drain. Cancelling does not roll back bytes already verified or migrated, and
// a terminal job is returned unchanged.
func (c *RustfsAdmin) ControlIntegrityJob(ctx context.Context, bucket, jobID, operation string) (IntegrityJob, error) {
	body, err := json.Marshal(map[string]string{"operation": operation})
	if err != nil {
		return IntegrityJob{}, err
	}

	reqData := RequestData{
		Method:  "POST",
		RelPath: "integrity/" + url.PathEscape(bucket) + "/jobs/" + url.PathEscape(jobID) + "/control",
		Content: body,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		// NoSuchKey on a missing job is not a capability gap.
		return IntegrityJob{}, err
	}
	defer drainClose(resp)

	var job IntegrityJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return IntegrityJob{}, err
	}
	return job, nil
}

// integrityFeatureError converts the server's "route not implemented" answers.
// The bucket-scoped integrity routes answer 404 NoSuchBucket for a missing
// bucket, so that code stays a plain API error instead of a capability gap;
// every other 404 means the route was never registered.
func (c *RustfsAdmin) integrityFeatureError(ctx context.Context, err error, feature Feature) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.StatusCode == http.StatusNotImplemented ||
		apiErr.StatusCode == http.StatusMethodNotAllowed ||
		strings.EqualFold(apiErr.Code, "NotImplemented") ||
		(apiErr.StatusCode == http.StatusNotFound && !IsAPIErrorCode(err, "NoSuchBucket")) {
		return c.featureError(ctx, err, feature)
	}
	return err
}

func init() {
	registerFeature(FeatureIntegrityReadiness, featureSpec{
		route: "GET /rustfs/admin/v3/integrity/readiness",
	})
	registerFeature(FeatureIntegrityInventory, featureSpec{
		route: "GET /rustfs/admin/v3/integrity/{bucket}/inventory",
	})
	registerFeature(FeatureIntegrityJobs, featureSpec{
		route: "POST /rustfs/admin/v3/integrity/{bucket}/jobs",
	})
}
