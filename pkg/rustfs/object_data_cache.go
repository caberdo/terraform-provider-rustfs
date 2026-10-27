// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// FeatureObjectDataCacheStats is the object data cache statistics read
// (GET /rustfs/admin/v3/object-data-cache/stats).
const FeatureObjectDataCacheStats Feature = "object data cache statistics"

// FeatureObjectDataCacheFlush is the operator-driven cache invalidation
// (POST /rustfs/admin/v3/object-data-cache/flush).
const FeatureObjectDataCacheFlush Feature = "object data cache flush"

// ObjectDataCacheStats is the stats snapshot the cache adapter reports. A
// deployment without a cache answers mode "disabled" with zero counters.
type ObjectDataCacheStats struct {
	Mode                 string `json:"mode"`
	Disabled             bool   `json:"disabled"`
	Entries              uint64 `json:"entries"`
	Lookups              uint64 `json:"lookups"`
	Hits                 uint64 `json:"hits"`
	Fills                uint64 `json:"fills"`
	Invalidations        uint64 `json:"invalidations"`
	InflightFills        uint64 `json:"inflight_fills"`
	SingleflightJoins    uint64 `json:"singleflight_joins"`
	MemoryPressureEvents uint64 `json:"memory_pressure_events"`

	RawJSON string `json:"-"`
}

// ObjectDataCacheFlushResult is the answer to a flush. Scope is "all", "bucket"
// or "object"; Outcome is "removed" or "noop"; RemovedKeys is what the
// invalidation dropped.
type ObjectDataCacheFlushResult struct {
	Scope       string  `json:"scope"`
	Bucket      *string `json:"bucket"`
	Object      *string `json:"object"`
	Outcome     string  `json:"outcome"`
	RemovedKeys uint64  `json:"removed_keys"`

	RawJSON string `json:"-"`
}

// GetObjectDataCacheStats reads GET /v3/object-data-cache/stats.
func (c *RustfsAdmin) GetObjectDataCacheStats(ctx context.Context) (ObjectDataCacheStats, error) {
	reqData := RequestData{
		Method:  http.MethodGet,
		RelPath: "object-data-cache/stats",
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return ObjectDataCacheStats{}, c.featureError(ctx, err, FeatureObjectDataCacheStats)
	}
	defer drainClose(resp)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ObjectDataCacheStats{}, err
	}

	result := ObjectDataCacheStats{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ObjectDataCacheStats{}, fmt.Errorf("decode object data cache statistics: %w", err)
	}
	result.RawJSON = string(raw)
	return result, nil
}

// FlushObjectDataCache drops cached object bodies. An empty bucket flushes the
// whole cache; a bucket flushes that bucket; a bucket plus object flushes one
// identity. The server rejects an object without a bucket.
func (c *RustfsAdmin) FlushObjectDataCache(ctx context.Context, bucket, object string) (ObjectDataCacheFlushResult, error) {
	if object != "" && bucket == "" {
		return ObjectDataCacheFlushResult{}, errors.New("an object flush requires a bucket")
	}

	query := url.Values{}
	if bucket != "" {
		query.Set("bucket", bucket)
	}
	if object != "" {
		query.Set("object", object)
	}

	reqData := RequestData{
		Method:      http.MethodPost,
		RelPath:     "object-data-cache/flush",
		QueryValues: query,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return ObjectDataCacheFlushResult{}, c.featureError(ctx, err, FeatureObjectDataCacheFlush)
	}
	defer drainClose(resp)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ObjectDataCacheFlushResult{}, err
	}

	result := ObjectDataCacheFlushResult{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ObjectDataCacheFlushResult{}, fmt.Errorf("decode object data cache flush: %w", err)
	}
	result.RawJSON = string(raw)
	return result, nil
}

func init() {
	registerFeature(FeatureObjectDataCacheStats, featureSpec{
		route: "GET /rustfs/admin/v3/object-data-cache/stats",
	})
	registerFeature(FeatureObjectDataCacheFlush, featureSpec{
		route: "POST /rustfs/admin/v3/object-data-cache/flush",
	})
}
