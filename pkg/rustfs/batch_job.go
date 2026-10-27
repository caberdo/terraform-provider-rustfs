// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
)

// FeatureBatchJobs is the batch-job listing read by the rustfs_batch_jobs data
// source (GET /rustfs/admin/v3/list-jobs).
const FeatureBatchJobs Feature = "batch jobs"

// FeatureBatchJobDescribe and FeatureBatchJobStatus are the single-job routes.
// They are registered separately so a capability gap names the exact route
// that is missing instead of the list route.
const (
	FeatureBatchJobDescribe Feature = "batch job describe"
	FeatureBatchJobStatus   Feature = "batch job status"
)

// BatchJobs is the answer of GET /rustfs/admin/v3/list-jobs.
//
// Each entry in Jobs is kept as the exact JSON document the server returned:
// MinIO's BatchJobInfo shape is large and version-dependent, and RustFS has no
// batch-job engine to run one, so the provider deliberately does not invent a
// typed schema for it.
type BatchJobs struct {
	Jobs []json.RawMessage `json:"jobs"`

	// Raw holds the unparsed response body so callers can expose the exact
	// JSON as returned by the server.
	Raw json.RawMessage `json:"-"`
}

// ListBatchJobs returns the batch jobs the server knows about from
// GET /rustfs/admin/v3/list-jobs. jobType optionally filters the list to one
// of replicate, keyrotate or expire; an empty value lists every type.
//
// RustFS ships the MinIO-compatible route without a batch-job execution
// backend, so the list is empty on every release that implements it.
func (c *RustfsAdmin) ListBatchJobs(ctx context.Context, jobType string) (BatchJobs, error) {
	query := url.Values{}
	if jobType != "" {
		query.Set("jobType", jobType)
	}

	resp, err := c.doRequest(ctx, RequestData{
		Method:      "GET",
		RelPath:     "list-jobs",
		QueryValues: query,
	})
	if err != nil {
		return BatchJobs{}, c.featureErrorPreservingCode(ctx, err, FeatureBatchJobs)
	}
	defer drainClose(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return BatchJobs{}, err
	}

	result := BatchJobs{Raw: json.RawMessage(body)}
	if err := json.Unmarshal(body, &result); err != nil {
		return BatchJobs{}, err
	}
	if result.Jobs == nil {
		result.Jobs = []json.RawMessage{}
	}
	return result, nil
}

// DescribeBatchJob returns the job definition of one batch job from
// GET /rustfs/admin/v3/describe-job. The document is returned verbatim
// because the provider does not own the MinIO batch-job schema.
func (c *RustfsAdmin) DescribeBatchJob(ctx context.Context, jobID string) (json.RawMessage, error) {
	return c.batchJobDocument(ctx, "describe-job", jobID, FeatureBatchJobDescribe)
}

// StatusBatchJob returns the runtime status of one batch job from
// GET /rustfs/admin/v3/status-job, verbatim.
func (c *RustfsAdmin) StatusBatchJob(ctx context.Context, jobID string) (json.RawMessage, error) {
	return c.batchJobDocument(ctx, "status-job", jobID, FeatureBatchJobStatus)
}

func (c *RustfsAdmin) batchJobDocument(ctx context.Context, relPath, jobID string, feature Feature) (json.RawMessage, error) {
	if jobID == "" {
		return nil, errors.New("job ID is required")
	}

	resp, err := c.doRequest(ctx, RequestData{
		Method:      "GET",
		RelPath:     relPath,
		QueryValues: url.Values{"jobId": {jobID}},
	})
	if err != nil {
		if IsNoSuchBatchJob(err) {
			return nil, err
		}
		return nil, c.featureErrorPreservingCode(ctx, err, feature)
	}
	defer drainClose(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("the server returned an empty batch job document")
	}
	return json.RawMessage(body), nil
}

// IsNoSuchBatchJob reports whether err is the batch-job routes' answer for a
// job id that cannot exist. RustFS answers NoSuchKey with the exact message
// "no such batch job: <id>"; NoSuchJob is accepted as well so the matcher
// keeps working if the server adopts MinIO's dedicated code. Both are
// structured codes, never a substring of the raw payload.
func IsNoSuchBatchJob(err error) bool {
	return IsAPIErrorCode(err, "NoSuchKey", "NoSuchJob")
}

func init() {
	registerFeature(FeatureBatchJobs, featureSpec{
		route: "GET /rustfs/admin/v3/list-jobs",
		// The route is registered and implemented in the tagged RustFS source
		// for 1.0.1 through 1.0.3, so there is no version floor to advertise.
		// A build that does not implement it produces a capability error
		// naming the exact server version.
	})
	registerFeature(FeatureBatchJobDescribe, featureSpec{
		route: "GET /rustfs/admin/v3/describe-job",
	})
	registerFeature(FeatureBatchJobStatus, featureSpec{
		route: "GET /rustfs/admin/v3/status-job",
	})
}
