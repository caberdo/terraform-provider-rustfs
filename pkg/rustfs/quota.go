// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
)

// FeatureQuotaStats is the quota subsystem's view of one bucket's limit and
// usage (GET /rustfs/admin/v3/quota-stats/{bucket}).
const FeatureQuotaStats Feature = "bucket quota stats"

// FeatureQuotaCheck is the read-only quota admission evaluation
// (POST /rustfs/admin/v3/quota-check/{bucket}).
const FeatureQuotaCheck Feature = "bucket quota check"

// QuotaStats is the answer of GET /rustfs/admin/v3/quota-stats/{bucket}.
//
// A nil field means the server could not report that value, which happens when
// the bucket is admitted by the reservation ledger and no scanner usage is
// available yet. Null must never be turned into zero: zero usage is a real
// answer and a quota limit of zero means "no quota configured".
type QuotaStats struct {
	Bucket          string   `json:"bucket"`
	QuotaLimit      *uint64  `json:"quota_limit"`
	CurrentUsage    *uint64  `json:"current_usage"`
	RemainingQuota  *uint64  `json:"remaining_quota"`
	UsagePercentage *float64 `json:"usage_percentage"`
}

// QuotaCheck is the answer of POST /rustfs/admin/v3/quota-check/{bucket}.
//
// The route only evaluates an operation against the bucket's quota; it writes
// nothing and changes no usage.
type QuotaCheck struct {
	Bucket         string  `json:"bucket"`
	OperationType  string  `json:"operation_type"`
	OperationSize  uint64  `json:"operation_size"`
	Allowed        bool    `json:"allowed"`
	CurrentUsage   *uint64 `json:"current_usage"`
	QuotaLimit     *uint64 `json:"quota_limit"`
	RemainingQuota *uint64 `json:"remaining_quota"`
}

type Quota struct {
	Bucket     string `json:"bucket"`
	Quota      int    `json:"quota"` //Size of the thing
	Quota_Type string `json:"quota_type"`
}

func (c *RustfsAdmin) ReadQuota(ctx context.Context, bucket string) (quota Quota, err error) {
	req_data := RequestData{
		Method:  "GET",
		RelPath: "quota/" + bucket,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return quota, err
	}
	defer drainClose(resp)
	err = json.NewDecoder(resp.Body).Decode(&quota)
	return quota, err
}

func (c *RustfsAdmin) SetQuota(ctx context.Context, new Quota) (quota Quota, err error) {
	new.Quota_Type = "HARD"
	bytes, err := json.Marshal(new)
	if err != nil {
		return Quota{}, err
	}
	req_data := RequestData{
		Method:  "PUT",
		RelPath: "quota/" + new.Bucket,
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return Quota{}, err
	}
	defer drainClose(resp)
	err = json.NewDecoder(resp.Body).Decode(&quota)
	return quota, err
}

func (c *RustfsAdmin) DeletQuota(ctx context.Context, bucket string) (err error) {
	req_data := RequestData{
		Method:  "DELETE",
		RelPath: "quota/" + bucket,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}

// GetQuotaStats returns the quota subsystem's view of a bucket's limit and
// usage from GET /rustfs/admin/v3/quota-stats/{bucket}.
//
// The route reads bucket quota metadata and the reservation ledger; it does not
// scan the namespace. A missing bucket is reported as NoSuchBucket, so the
// caller sees the server's error instead of zeroed fields.
func (c *RustfsAdmin) GetQuotaStats(ctx context.Context, bucket string) (QuotaStats, error) {
	if bucket == "" {
		return QuotaStats{}, errors.New("bucket name is required")
	}

	resp, err := c.doRequest(ctx, RequestData{
		Method:  "GET",
		RelPath: "quota-stats/" + bucket,
	})
	if err != nil {
		return QuotaStats{}, c.featureErrorPreservingCode(ctx, err, FeatureQuotaStats)
	}
	defer drainClose(resp)

	var stats QuotaStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return QuotaStats{}, err
	}
	return stats, nil
}

// CheckQuota evaluates whether an operation of operationSize bytes would be
// admitted against the bucket's quota, from
// POST /rustfs/admin/v3/quota-check/{bucket}.
//
// operationType is one of PUT, POST, COPY or DELETE. RustFS treats an unknown
// value as PUT. The call is read-only: it asks the quota checker for a
// decision, it does not reserve bytes or modify the bucket.
func (c *RustfsAdmin) CheckQuota(ctx context.Context, bucket, operationType string, operationSize int64) (QuotaCheck, error) {
	if bucket == "" {
		return QuotaCheck{}, errors.New("bucket name is required")
	}
	if operationType == "" {
		return QuotaCheck{}, errors.New("operation type is required")
	}
	if operationSize < 0 {
		return QuotaCheck{}, errors.New("operation size must not be negative")
	}

	body, err := json.Marshal(struct {
		OperationType string `json:"operation_type"`
		OperationSize int64  `json:"operation_size"`
	}{
		OperationType: operationType,
		OperationSize: operationSize,
	})
	if err != nil {
		return QuotaCheck{}, err
	}

	resp, err := c.doRequest(ctx, RequestData{
		Method:  "POST",
		RelPath: "quota-check/" + bucket,
		Content: body,
	})
	if err != nil {
		return QuotaCheck{}, c.featureErrorPreservingCode(ctx, err, FeatureQuotaCheck)
	}
	defer drainClose(resp)

	var check QuotaCheck
	if err := json.NewDecoder(resp.Body).Decode(&check); err != nil {
		return QuotaCheck{}, err
	}
	return check, nil
}

func init() {
	registerFeature(FeatureQuotaStats, featureSpec{
		route: "GET /rustfs/admin/v3/quota-stats/{bucket}",
		// The route is implemented in the tagged RustFS source for 1.0.1
		// through 1.0.3, so there is no version floor to advertise. A build that
		// does not serve it produces a capability error naming the version.
	})
	registerFeature(FeatureQuotaCheck, featureSpec{
		route: "POST /rustfs/admin/v3/quota-check/{bucket}",
	})
}
