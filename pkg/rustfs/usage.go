// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
)

// FeatureBucketUsage is the per-bucket prefix usage read by the
// rustfs_bucket_usage data source (GET /rustfs/admin/v3/usage/{bucket}).
const FeatureBucketUsage Feature = "bucket usage"

func init() {
	registerFeature(FeatureBucketUsage, featureSpec{
		route: "GET /rustfs/admin/v3/usage/{bucket}",
		// The route is registered and implemented in the tagged RustFS source
		// for 1.0.1 through 1.0.3, so there is no version floor to advertise.
	})
}

// BucketUsageSummary is the flattened accounting of one bucket or prefix.
type BucketUsageSummary struct {
	Size          int64 `json:"size"`
	Objects       int64 `json:"objects"`
	Versions      int64 `json:"versions"`
	DeleteMarkers int64 `json:"deleteMarkers"`
}

// BucketSubPrefixUsage is one first-level sub-prefix row.
type BucketSubPrefixUsage struct {
	Prefix string             `json:"prefix"`
	Usage  BucketUsageSummary `json:"usage"`
}

// BucketUsage is the answer of GET /v3/usage/{bucket}.
//
// The numbers come from the scanner's per-set usage caches, not from a live
// walk: they lag the last scanner cycle and the endpoint itself caches a
// computed answer for up to 30 seconds. SetsReporting below SetsTotal means at
// least one erasure set could not contribute, so the counters are a lower
// bound. RawJSON keeps the exact body so a newer server field stays available
// while the typed fields stay limited to what the provider documents.
type BucketUsage struct {
	Bucket             string
	Prefix             string
	Usage              BucketUsageSummary
	Compacted          bool
	SubPrefixesPartial bool
	Truncated          bool
	SubPrefixes        []BucketSubPrefixUsage
	SetsReporting      int
	SetsTotal          int
	LastUpdateUnixSecs *int64
	RawJSON            string
}

type bucketUsageEnvelope struct {
	Bucket             string                 `json:"bucket"`
	Prefix             string                 `json:"prefix"`
	Usage              BucketUsageSummary     `json:"usage"`
	Compacted          bool                   `json:"compacted"`
	SubPrefixesPartial bool                   `json:"subPrefixesPartial"`
	Truncated          bool                   `json:"truncated"`
	SubPrefixes        []BucketSubPrefixUsage `json:"subPrefixes"`
	SetsReporting      int                    `json:"setsReporting"`
	SetsTotal          int                    `json:"setsTotal"`
	LastUpdateUnixSecs *int64                 `json:"lastUpdateUnixSecs"`
}

// GetBucketUsage reads GET /v3/usage/{bucket}.
//
// prefix limits the answer to one prefix ("" is the whole bucket) and
// maxEntries bounds the sub-prefix breakdown (the server defaults to 1000 and
// clamps to 1..10000). A bucket that does not exist is not reported by this
// route: it answers zeroed counters, so the caller has to confirm existence
// itself.
func (c *RustfsAdmin) GetBucketUsage(ctx context.Context, bucket, prefix string, maxEntries int) (BucketUsage, error) {
	if bucket == "" {
		return BucketUsage{}, errors.New("bucket is required")
	}
	if maxEntries < 0 {
		return BucketUsage{}, fmt.Errorf("max entries must not be negative: %d", maxEntries)
	}

	query := make(url.Values)
	if prefix != "" {
		query.Set("prefix", prefix)
	}
	if maxEntries > 0 {
		query.Set("max-entries", strconv.Itoa(maxEntries))
	}

	reqData := RequestData{
		Method:      "GET",
		RelPath:     "usage/" + url.PathEscape(bucket),
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return BucketUsage{}, c.featureError(ctx, err, FeatureBucketUsage)
	}
	defer drainClose(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return BucketUsage{}, err
	}

	var envelope bucketUsageEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return BucketUsage{}, fmt.Errorf("decode bucket usage: %w", err)
	}

	return BucketUsage{
		Bucket:             envelope.Bucket,
		Prefix:             envelope.Prefix,
		Usage:              envelope.Usage,
		Compacted:          envelope.Compacted,
		SubPrefixesPartial: envelope.SubPrefixesPartial,
		Truncated:          envelope.Truncated,
		SubPrefixes:        envelope.SubPrefixes,
		SetsReporting:      envelope.SetsReporting,
		SetsTotal:          envelope.SetsTotal,
		LastUpdateUnixSecs: envelope.LastUpdateUnixSecs,
		RawJSON:            string(body),
	}, nil
}
