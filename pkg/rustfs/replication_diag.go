// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
)

// FeatureReplicationDiff is the on-demand replication diff read by the
// rustfs_replication_diff data source (POST /rustfs/admin/v3/replication/diff).
const FeatureReplicationDiff Feature = "replication diff"

// FeatureReplicationMRF is the failed-replication backlog read by the
// rustfs_replication_mrf data source (GET /rustfs/admin/v3/replication/mrf).
const FeatureReplicationMRF Feature = "replication MRF backlog"

// ReplicationDiffEntry is one object version whose replication is still
// pending or has failed. Field names mirror madmin-go's DiffInfo, which is the
// contract mc replicate diff decodes; Size is RustFS's extension key.
type ReplicationDiffEntry struct {
	Object            string  `json:"object"`
	VersionID         *string `json:"versionId"`
	Size              int64   `json:"Size"`
	IsDeleteMarker    bool    `json:"deletemarker"`
	ReplicationStatus string  `json:"rStatus"`
	LastModified      *string `json:"lastModified"`
}

// ReplicationDiff is the aggregate answer of
// POST /rustfs/admin/v3/replication/diff?aggregate=true.
//
// The aggregate shape is a RustFS extension: without it the route streams one
// DiffInfo document per line and cannot report scan truncation. The provider
// always asks for the aggregate shape so is_truncated and scanned_versions are
// available.
type ReplicationDiff struct {
	Entries         []ReplicationDiffEntry `json:"Entries"`
	IsTruncated     bool                   `json:"IsTruncated"`
	ScannedVersions int64                  `json:"ScannedVersions"`

	// ReplicationConfigured is false when the server reports that the bucket
	// has no replication configuration. Nothing can be pending in that case,
	// so the diff is empty rather than an error.
	ReplicationConfigured bool `json:"-"`

	// Raw holds the unparsed aggregate response body.
	Raw json.RawMessage `json:"-"`
}

// ReplicationMRFTarget is one remote target's failed and durable backlog
// counters from GET /rustfs/admin/v3/replication/mrf?aggregate=true.
type ReplicationMRFTarget struct {
	ARN              string `json:"ARN"`
	FailedCount      int64  `json:"FailedCount"`
	FailedSize       int64  `json:"FailedSize"`
	DurableCount     int64  `json:"DurableCount"`
	DurableSize      int64  `json:"DurableSize"`
	ObservationScope string `json:"ObservationScope"`
}

// ReplicationMRF is the aggregate answer of
// GET /rustfs/admin/v3/replication/mrf?aggregate=true.
//
// The default response is a madmin-compatible per-object stream that
// enumerates object names and version ids and requires the ReplicationDiff
// admin action. The aggregate shape carries counters only, requires the
// narrower metrics action, and reports whether the durable backlog and
// runtime statistics were readable.
type ReplicationMRF struct {
	Bucket                           string                 `json:"Bucket"`
	Targets                          []ReplicationMRFTarget `json:"Targets"`
	TotalFailedCount                 int64                  `json:"TotalFailedCount"`
	TotalFailedSize                  int64                  `json:"TotalFailedSize"`
	QueuedCount                      int64                  `json:"QueuedCount"`
	QueuedSize                       int64                  `json:"QueuedSize"`
	PerObjectEntriesAvailable        bool                   `json:"PerObjectEntriesAvailable"`
	RuntimeStatsAvailable            bool                   `json:"RuntimeStatsAvailable"`
	ClusterComplete                  bool                   `json:"ClusterComplete"`
	ObservedNodeCount                uint32                 `json:"ObservedNodeCount"`
	ExpectedNodeCount                uint32                 `json:"ExpectedNodeCount"`
	DurableBacklogAvailable          bool                   `json:"DurableBacklogAvailable"`
	DurableCount                     int64                  `json:"DurableCount"`
	DurableSize                      int64                  `json:"DurableSize"`
	PerTargetDurableEntriesAvailable bool                   `json:"PerTargetDurableEntriesAvailable"`

	// ReplicationConfigured is false when the server reports that the bucket
	// has no replication configuration. There is no backlog in that case, so
	// the counters are zero rather than an error.
	ReplicationConfigured bool `json:"-"`

	// Raw holds the unparsed aggregate response body.
	Raw json.RawMessage `json:"-"`
}

// ReplicationDiff computes the object versions of bucket that are still
// pending or failed, optionally restricted to prefix, through
// POST /rustfs/admin/v3/replication/diff.
func (c *RustfsAdmin) ReplicationDiff(ctx context.Context, bucket, prefix string) (ReplicationDiff, error) {
	if bucket == "" {
		return ReplicationDiff{}, errors.New("bucket is required")
	}

	query := url.Values{
		"bucket":    {bucket},
		"aggregate": {"true"},
	}
	if prefix != "" {
		query.Set("prefix", prefix)
	}

	resp, err := c.doRequest(ctx, RequestData{
		Method:      "POST",
		RelPath:     "replication/diff",
		QueryValues: query,
	})
	if err != nil {
		if IsAPIErrorCode(err, "ReplicationConfigurationNotFoundError") {
			return ReplicationDiff{
				Entries:               []ReplicationDiffEntry{},
				ReplicationConfigured: false,
			}, nil
		}
		return ReplicationDiff{}, c.featureErrorPreservingCode(ctx, err, FeatureReplicationDiff)
	}
	defer drainClose(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ReplicationDiff{}, err
	}
	result := ReplicationDiff{ReplicationConfigured: true, Raw: json.RawMessage(body)}
	if err := json.Unmarshal(body, &result); err != nil {
		return ReplicationDiff{}, err
	}
	if result.Entries == nil {
		result.Entries = []ReplicationDiffEntry{}
	}
	return result, nil
}

// ReplicationMRF returns the failed-replication backlog counters of bucket
// from GET /rustfs/admin/v3/replication/mrf?aggregate=true.
func (c *RustfsAdmin) ReplicationMRF(ctx context.Context, bucket string) (ReplicationMRF, error) {
	if bucket == "" {
		return ReplicationMRF{}, errors.New("bucket is required")
	}

	resp, err := c.doRequest(ctx, RequestData{
		Method:  "GET",
		RelPath: "replication/mrf",
		QueryValues: url.Values{
			"bucket":    {bucket},
			"aggregate": {"true"},
		},
	})
	if err != nil {
		if IsAPIErrorCode(err, "ReplicationConfigurationNotFoundError") {
			return ReplicationMRF{
				Bucket:                bucket,
				Targets:               []ReplicationMRFTarget{},
				ReplicationConfigured: false,
			}, nil
		}
		return ReplicationMRF{}, c.featureErrorPreservingCode(ctx, err, FeatureReplicationMRF)
	}
	defer drainClose(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ReplicationMRF{}, err
	}
	result := ReplicationMRF{Bucket: bucket, ReplicationConfigured: true, Raw: json.RawMessage(body)}
	if err := json.Unmarshal(body, &result); err != nil {
		return ReplicationMRF{}, err
	}
	if result.Targets == nil {
		result.Targets = []ReplicationMRFTarget{}
	}
	sort.Slice(result.Targets, func(i, j int) bool {
		return result.Targets[i].ARN < result.Targets[j].ARN
	})
	return result, nil
}

func init() {
	registerFeature(FeatureReplicationDiff, featureSpec{
		route: "POST /rustfs/admin/v3/replication/diff",
		// The route is registered and implemented in the tagged RustFS source
		// for 1.0.1 through 1.0.3, so there is no version floor to advertise.
		// A build that does not implement it produces a capability error
		// naming the exact server version.
	})
	registerFeature(FeatureReplicationMRF, featureSpec{
		route: "GET /rustfs/admin/v3/replication/mrf",
	})
}
