// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Feature names for the ILM operations surfaced by the provider. Each route
// family is pinned separately so a server that implements one half of the
// surface still gets a diagnostic naming the route it is missing.
const (
	// FeatureILMTransition is a manual transition run and its durable job
	// (POST /rustfs/admin/v3/ilm/transition/run and
	// GET|DELETE /rustfs/admin/v3/ilm/transition/jobs/{job_id}).
	FeatureILMTransition Feature = "manual ILM transition jobs"

	// FeatureILMExpiryStatus is the lifecycle expiry worker status
	// (GET /rustfs/admin/v3/ilm/expiry/status).
	FeatureILMExpiryStatus Feature = "the ILM expiry status"

	// FeatureILMRecoveryRecords is the durable ILM recovery control inventory
	// (GET /rustfs/admin/v3/ilm/recovery/records).
	FeatureILMRecoveryRecords Feature = "ILM recovery records"
)

func init() {
	// The routes ship together from RustFS 1.0.0 on, which is older than the
	// oldest release the provider is tested against (1.0.1). A server below
	// that floor is refused with a version-aware diagnostic instead of an
	// opaque 404.
	minVersion := &Version{Major: 1, Minor: 0, Patch: 1}

	registerFeature(FeatureILMTransition, featureSpec{
		route:      "POST /rustfs/admin/v3/ilm/transition/run",
		minVersion: minVersion,
	})
	registerFeature(FeatureILMExpiryStatus, featureSpec{
		route:      "GET /rustfs/admin/v3/ilm/expiry/status",
		minVersion: minVersion,
	})
	registerFeature(FeatureILMRecoveryRecords, featureSpec{
		route:      "GET /rustfs/admin/v3/ilm/recovery/records",
		minVersion: minVersion,
	})
}

// ILMTransitionRunOptions is one manual transition run. Only Bucket is
// required; the zero value would transition the whole bucket against the
// bucket's own lifecycle configuration.
//
// MaxObjects and MaxDurationSeconds are the server-side budgets. The server
// defaults MaxObjects to 10000, accepts at most 100000 objects and at most
// 3600 seconds. Async asks the server for a durable job instead of running the
// enqueue synchronously.
type ILMTransitionRunOptions struct {
	Bucket             string
	Prefix             string
	Tier               string
	DryRun             bool
	MaxObjects         int64
	MaxDurationSeconds int64
	Async              bool
}

// ILMTransitionReport is the run report of a manual transition. The server
// returns it both for the run itself and for the durable job, so an operator
// reads the same counters whichever mode was used.
//
// Counters the server omits when zero are decoded as zero.
type ILMTransitionReport struct {
	Bucket                     string           `json:"bucket"`
	Prefix                     string           `json:"prefix"`
	Tier                       string           `json:"tier"`
	DryRun                     bool             `json:"dry_run"`
	LifecycleConfigFound       bool             `json:"lifecycle_config_found"`
	Scanned                    int64            `json:"scanned"`
	Eligible                   int64            `json:"eligible"`
	Enqueued                   int64            `json:"enqueued"`
	DryRunEligible             int64            `json:"dry_run_eligible"`
	SkippedNotTransition       int64            `json:"skipped_not_transition"`
	SkippedTier                int64            `json:"skipped_tier"`
	SkippedDeleteMarker        int64            `json:"skipped_delete_marker"`
	SkippedDirectory           int64            `json:"skipped_directory"`
	SkippedReplication         int64            `json:"skipped_replication"`
	SkippedAlreadyTransitioned int64            `json:"skipped_already_transitioned"`
	SkippedAlreadyInFlight     int64            `json:"skipped_already_in_flight"`
	SkippedQueueFull           int64            `json:"skipped_queue_full"`
	SkippedQueueClosed         int64            `json:"skipped_queue_closed"`
	SkippedQueueTimeout        int64            `json:"skipped_queue_timeout"`
	TransitionCompleted        int64            `json:"transition_completed"`
	TransitionFailed           int64            `json:"transition_failed"`
	TierFailure                int64            `json:"tier_failure"`
	TierFailureByReason        map[string]int64 `json:"tier_failure_by_reason"`
	TruncatedByLimit           bool             `json:"truncated_by_limit"`
	TruncatedByDuration        bool             `json:"truncated_by_duration"`
	Cancelled                  bool             `json:"cancelled"`
	ContinuationToken          string           `json:"continuation_token"`
}

// ILMTransitionQueueSnapshot is the transition worker queue pressure recorded
// with a durable job.
type ILMTransitionQueueSnapshot struct {
	QueueCapacity       int64 `json:"queue_capacity"`
	Queued              int64 `json:"queued"`
	Active              int64 `json:"active"`
	Workers             int64 `json:"workers"`
	QueueFull           int64 `json:"queue_full"`
	QueueSendTimeout    int64 `json:"queue_send_timeout"`
	CompensationPending int64 `json:"compensation_pending"`
	CompensationRunning int64 `json:"compensation_running"`
}

// ILMTransitionRunResponse is the server's answer to a manual transition run.
// Mode is "durable_job" when Async was requested and "enqueue_only" otherwise;
// only a durable job carries a JobID and the status/cancel endpoints.
type ILMTransitionRunResponse struct {
	State          string              `json:"state"`
	Mode           string              `json:"mode"`
	JobID          string              `json:"job_id"`
	StatusEndpoint string              `json:"status_endpoint"`
	CancelEndpoint string              `json:"cancel_endpoint"`
	Report         ILMTransitionReport `json:"report"`
}

// ILMTransitionJob is one durable manual transition job. Status is one of
// "running", "completed", "partial", "failed", "cancelled" or "unknown".
// A cancelled job is not rolled back: the objects already enqueued stay
// enqueued.
type ILMTransitionJob struct {
	Status               string                     `json:"status"`
	Mode                 string                     `json:"mode"`
	JobID                string                     `json:"job_id"`
	StatusEndpoint       string                     `json:"status_endpoint"`
	CancelEndpoint       string                     `json:"cancel_endpoint"`
	CancelRequested      bool                       `json:"cancel_requested"`
	Bucket               string                     `json:"bucket"`
	Prefix               string                     `json:"prefix"`
	Tier                 string                     `json:"tier"`
	DryRun               bool                       `json:"dry_run"`
	CreatedAtUnixNanos   int64                      `json:"created_at_unix_nanos"`
	UpdatedAtUnixNanos   int64                      `json:"updated_at_unix_nanos"`
	CompletedAtUnixNanos *int64                     `json:"completed_at_unix_nanos"`
	Report               ILMTransitionReport        `json:"report"`
	QueueSnapshot        ILMTransitionQueueSnapshot `json:"queue_snapshot"`
	FailureReason        string                     `json:"failure_reason"`
}

// RunILMTransition starts a manual transition run. With Async the server
// accepts a durable job and answers "accepted" together with its job id;
// otherwise it scans and enqueues synchronously and answers "completed" or
// "partial".
func (c *RustfsAdmin) RunILMTransition(ctx context.Context, options ILMTransitionRunOptions) (*ILMTransitionRunResponse, error) {
	query := url.Values{}
	query.Set("bucket", options.Bucket)
	if options.Prefix != "" {
		query.Set("prefix", options.Prefix)
	}
	if options.Tier != "" {
		query.Set("tier", options.Tier)
	}
	if options.DryRun {
		query.Set("dryRun", "true")
	}
	if options.MaxObjects > 0 {
		query.Set("maxObjects", strconv.FormatInt(options.MaxObjects, 10))
	}
	if options.MaxDurationSeconds > 0 {
		query.Set("maxDurationSeconds", strconv.FormatInt(options.MaxDurationSeconds, 10))
	}
	if options.Async {
		query.Set("async", "true")
	}

	reqData := RequestData{
		Method:      "POST",
		RelPath:     "ilm/transition/run",
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureError(ctx, err, FeatureILMTransition)
	}
	defer drainClose(resp)

	var body ILMTransitionRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return &body, nil
}

// ILMTransitionJob returns one durable transition job. A job the server no
// longer holds is reported as an error carrying NoSuchKey so the caller can
// treat it as absent.
func (c *RustfsAdmin) ILMTransitionJob(ctx context.Context, jobID string) (*ILMTransitionJob, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "ilm/transition/jobs/" + url.PathEscape(jobID),
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	var body ILMTransitionJob
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return &body, nil
}

// CancelILMTransitionJob asks the server to cancel a durable transition job.
// Cancelling is not a rollback: objects already enqueued or moved stay where
// they are. The provider never calls this on destroy.
func (c *RustfsAdmin) CancelILMTransitionJob(ctx context.Context, jobID string) error {
	reqData := RequestData{
		Method:  "DELETE",
		RelPath: "ilm/transition/jobs/" + url.PathEscape(jobID),
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}

// ILMFreshness is the scanner freshness block shared by the expiry status.
type ILMFreshness struct {
	State                 string `json:"state"`
	LastCycleEndUnixSecs  int64  `json:"last_cycle_end_unix_secs"`
	MaxExpectedAgeSeconds int64  `json:"max_expected_age_seconds"`
	Reason                string `json:"reason"`
}

// ILMLifecycleExpiryCounters is the expiry worker's queue and outcome counters.
type ILMLifecycleExpiryCounters struct {
	CurrentQueueCapacity int64 `json:"current_queue_capacity"`
	CurrentQueued        int64 `json:"current_queued"`
	CurrentActive        int64 `json:"current_active"`
	CurrentWorkers       int64 `json:"current_workers"`
	QueueMissed          int64 `json:"queue_missed"`
	ScannerQueued        int64 `json:"scanner_queued"`
	ScannerMissed        int64 `json:"scanner_missed"`
	ScannerBlocked       int64 `json:"scanner_blocked"`
	ScannerNotEnqueued   int64 `json:"scanner_not_enqueued"`
	DeleteFailed         int64 `json:"delete_failed"`
}

// ILMMaintenanceSource is one source feeding the scanner maintenance control.
type ILMMaintenanceSource struct {
	Source         string `json:"source"`
	State          string `json:"state"`
	Reason         string `json:"reason"`
	Backlog        int64  `json:"backlog"`
	CurrentChecked int64  `json:"current_checked"`
	CurrentQueued  int64  `json:"current_queued"`
	CurrentMissed  int64  `json:"current_missed"`
	LifetimeMissed int64  `json:"lifetime_missed"`
	PartialCycles  int64  `json:"partial_cycles"`
}

// ILMMaintenanceControl is the scanner maintenance control block.
type ILMMaintenanceControl struct {
	PrimaryControl string                 `json:"primary_control"`
	Sources        []ILMMaintenanceSource `json:"sources"`
}

// ILMDataMovementPause is the cluster-wide data-movement pause status.
type ILMDataMovementPause struct {
	Paused                   bool     `json:"paused"`
	Policy                   string   `json:"policy"`
	Reasons                  []string `json:"reasons"`
	StartedAtUnixSecs        int64    `json:"started_at_unix_secs"`
	DurationSeconds          int64    `json:"duration_seconds"`
	OperationEpoch           int64    `json:"operation_epoch"`
	MovementGeneration       int64    `json:"movement_generation"`
	MovementBacklogWorkItems int64    `json:"movement_backlog_work_items"`
	MovementBacklogEstimated bool     `json:"movement_backlog_estimated"`
}

// ILMCatchUpEstimate is the scanner's estimate of outstanding ILM work.
type ILMCatchUpEstimate struct {
	Estimated                 bool  `json:"estimated"`
	MovementWorkItems         int64 `json:"movement_work_items"`
	DirtyUsageBuckets         int64 `json:"dirty_usage_buckets"`
	DiscoveredExpiryItems     int64 `json:"discovered_expiry_items"`
	DiscoveredTransitionItems int64 `json:"discovered_transition_items"`
	UndiscoveredILMItemsKnown bool  `json:"undiscovered_ilm_items_known"`
	UsageBaselineUnixSecs     int64 `json:"usage_baseline_unix_secs"`
}

// ILMExpiryStatus is the lifecycle expiry worker status. The server owns the
// payload; fields this client does not model are preserved in Raw, which the
// data source exposes as JSON.
type ILMExpiryStatus struct {
	Enabled                            bool                       `json:"enabled"`
	DisabledReason                     string                     `json:"disabled_reason"`
	Freshness                          ILMFreshness               `json:"freshness"`
	LifecycleExpiry                    ILMLifecycleExpiryCounters `json:"lifecycle_expiry"`
	MaintenanceControl                 ILMMaintenanceControl      `json:"maintenance_control"`
	CurrentCycleLifecycleExpiryActions int64                      `json:"current_cycle_lifecycle_expiry_actions"`
	LastCycleLifecycleExpiryActions    int64                      `json:"last_cycle_lifecycle_expiry_actions"`
	DataMovementPause                  ILMDataMovementPause       `json:"data_movement_pause"`
	CatchUpEstimate                    ILMCatchUpEstimate         `json:"catch_up_estimate"`
	Raw                                json.RawMessage            `json:"-"`
}

// ExpiryStatus returns the lifecycle expiry worker status
// (GET /rustfs/admin/v3/ilm/expiry/status).
func (c *RustfsAdmin) ILMExpiryStatus(ctx context.Context) (*ILMExpiryStatus, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "ilm/expiry/status",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureError(ctx, err, FeatureILMExpiryStatus)
	}
	defer drainClose(resp)

	raw, err := readAll(resp)
	if err != nil {
		return nil, err
	}
	var body ILMExpiryStatus
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	body.Raw = raw
	return &body, nil
}

// ILMRecoveryRecordQuery selects a page of ILM recovery controls. Protocol is
// required by the server; Classification, Limit and Marker are optional and
// Limit defaults to 100 (the server accepts 1..1000).
type ILMRecoveryRecordQuery struct {
	Protocol       string
	Classification string
	Limit          int64
	Marker         string
}

// ILMRecoveryRecord is one durable ILM recovery control. Timestamps are Unix
// nanoseconds; the optional ones point at a zero Go value when the server
// omits them.
type ILMRecoveryRecord struct {
	ControlID               string `json:"control_id"`
	Protocol                string `json:"protocol"`
	Classification          string `json:"classification"`
	Schema                  string `json:"schema"`
	Revision                int64  `json:"revision"`
	AttemptCount            int64  `json:"attempt_count"`
	ConsecutiveFailureCount int64  `json:"consecutive_failure_count"`
	FirstSeenAtUnixNanos    int64  `json:"first_seen_at_unix_nanos"`
	FirstFailureAtUnixNanos *int64 `json:"first_failure_at_unix_nanos"`
	LastFailureAtUnixNanos  *int64 `json:"last_failure_at_unix_nanos"`
	NextAttemptAtUnixNanos  *int64 `json:"next_attempt_at_unix_nanos"`
	LastErrorCode           string `json:"last_error_code"`
	SourceSchema            string `json:"source_schema"`
	SourceGenerationSHA256  string `json:"source_generation_sha256"`
	CopySetSHA256           string `json:"copy_set_sha256"`
	SourceCopyCount         int64  `json:"source_copy_count"`
}

// ILMRecoveryRecordPage is one page of recovery controls. Truncated reports
// that more records match; Incomplete reports that the fleet could not be
// inventoried in full.
type ILMRecoveryRecordPage struct {
	Records    []ILMRecoveryRecord `json:"records"`
	NextMarker string              `json:"next_marker"`
	Truncated  bool                `json:"truncated"`
	Incomplete bool                `json:"incomplete"`
	Raw        json.RawMessage     `json:"-"`
}

// ListILMRecoveryRecords returns one page of durable ILM recovery controls
// (GET /rustfs/admin/v3/ilm/recovery/records).
func (c *RustfsAdmin) ListILMRecoveryRecords(ctx context.Context, query ILMRecoveryRecordQuery) (*ILMRecoveryRecordPage, error) {
	values := url.Values{}
	values.Set("protocol", query.Protocol)
	if query.Classification != "" {
		values.Set("classification", query.Classification)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.FormatInt(query.Limit, 10))
	}
	if query.Marker != "" {
		values.Set("marker", query.Marker)
	}

	reqData := RequestData{
		Method:      "GET",
		RelPath:     "ilm/recovery/records",
		QueryValues: values,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureError(ctx, err, FeatureILMRecoveryRecords)
	}
	defer drainClose(resp)

	raw, err := readAll(resp)
	if err != nil {
		return nil, err
	}
	var body ILMRecoveryRecordPage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	body.Raw = raw
	return &body, nil
}

// InspectILMRecoveryRecord returns one recovery control, including the
// observation receipts the server only issues through this route. The payload
// is returned as raw JSON because it carries short-lived, action-specific
// receipts rather than a stable read model.
func (c *RustfsAdmin) InspectILMRecoveryRecord(ctx context.Context, controlID string) (json.RawMessage, error) {
	return c.ilmRawRequest(ctx, "GET", "ilm/recovery/records/"+url.PathEscape(controlID), nil, nil)
}

// InspectILMTransitionReconcile reads one transition transaction for operator
// reconciliation (GET /rustfs/admin/v3/ilm/transition/reconcile/{id}). It never
// mutates object state.
func (c *RustfsAdmin) InspectILMTransitionReconcile(ctx context.Context, transactionID string) (json.RawMessage, error) {
	return c.ilmRawRequest(ctx, "GET", "ilm/transition/reconcile/"+url.PathEscape(transactionID), nil, nil)
}

// ApplyILMTransitionReconcile runs one operator reconciliation
// (POST /rustfs/admin/v3/ilm/transition/reconcile/{id}). It mutates object
// state on the remote backend, so it is deliberately not part of any Terraform
// read path. action is "delete_candidate" (with a non-empty remoteVersionID)
// or "finalize_missing".
func (c *RustfsAdmin) ApplyILMTransitionReconcile(ctx context.Context, transactionID, action, remoteVersionID string) (json.RawMessage, error) {
	body, err := json.Marshal(struct {
		Action          string `json:"action"`
		Confirm         bool   `json:"confirm"`
		RemoteVersionID string `json:"remote_version_id,omitempty"`
	}{
		Action:          action,
		Confirm:         true,
		RemoteVersionID: remoteVersionID,
	})
	if err != nil {
		return nil, err
	}
	return c.ilmRawRequest(ctx, "POST", "ilm/transition/reconcile/"+url.PathEscape(transactionID), nil, body)
}

// InspectILMLegacyTransitionState reads the legacy transition-state record of
// one object version (GET /rustfs/admin/v3/ilm/transition/state/reconcile).
// versionID must be the local version id, or the literal "null" for an
// unversioned object.
func (c *RustfsAdmin) InspectILMLegacyTransitionState(ctx context.Context, bucket, object, versionID string) (json.RawMessage, error) {
	values := url.Values{}
	values.Set("bucket", bucket)
	values.Set("object", object)
	values.Set("versionId", versionID)
	return c.ilmRawRequest(ctx, "GET", "ilm/transition/state/reconcile", values, nil)
}

// ilmRawRequest issues an ILM admin request and returns the response body
// unchanged, for routes whose payload is owned by the server.
func (c *RustfsAdmin) ilmRawRequest(ctx context.Context, method, relPath string, query url.Values, body []byte) (json.RawMessage, error) {
	reqData := RequestData{
		Method:      method,
		RelPath:     relPath,
		QueryValues: query,
		Content:     body,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	raw, err := readAll(resp)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// readAll reads a response body, tolerating a nil body so an empty 200 from an
// older server decodes as zero values rather than panicking.
func readAll(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	return io.ReadAll(resp.Body)
}
