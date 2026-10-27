// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/url"
)

// FeaturePoolDecommission is the pool drain and retire capability served by
// the decommission routes on RustFS 1.0.1 and later.
const FeaturePoolDecommission Feature = "pool decommission"

// PoolDecommissionStatus is one pool's decommission state as reported by
// GET /rustfs/admin/v3/decommission/status.
//
// The list route answers {"pools":[...]}; the same route with ?pool=<cmdline>
// answers one of these objects directly.
type PoolDecommissionStatus struct {
	ID         int64                 `json:"id"`
	Cmdline    string                `json:"cmdline"`
	Status     string                `json:"status"`
	PoolStatus string                `json:"poolStatus"`
	Info       *PoolDecommissionInfo `json:"decommissionInfo"`
}

// PoolDecommissionInfo is the progress of a pool drain. It is null while a
// pool has never been decommissioned.
type PoolDecommissionInfo struct {
	// StartTime is null on a canceled drain: RustFS clears it but keeps the
	// counters.
	StartTime                   *string  `json:"startTime"`
	StartSize                   int64    `json:"startSize"`
	TotalSize                   int64    `json:"totalSize"`
	CurrentSize                 int64    `json:"currentSize"`
	Complete                    bool     `json:"complete"`
	Failed                      bool     `json:"failed"`
	Canceled                    bool     `json:"canceled"`
	Queued                      bool     `json:"queued"`
	QueuedBuckets               []string `json:"queuedBuckets"`
	DecommissionedBuckets       []string `json:"decommissionedBuckets"`
	Bucket                      string   `json:"bucket"`
	Prefix                      string   `json:"prefix"`
	Object                      string   `json:"object"`
	Stage                       string   `json:"stage"`
	ObjectsDecommissioned       int64    `json:"objectsDecommissioned"`
	ObjectsDecommissionedFailed int64    `json:"objectsDecommissionedFailed"`
	BytesDecommissioned         int64    `json:"bytesDecommissioned"`
	BytesDecommissionedFailed   int64    `json:"bytesDecommissionedFailed"`
	WaitingReason               *string  `json:"waitingReason"`
}

// decommissionStatusResponse is the list form of the status route.
type decommissionStatusResponse struct {
	Pools []PoolDecommissionStatus `json:"pools"`
}

// DecommissionStatuses returns every storage pool's decommission state from
// GET /rustfs/admin/v3/decommission/status.
//
// RustFS answers 501 NotImplemented for this route on a single-pool
// deployment: decommission needs at least two pools, and the route is only
// served when the persisted layout is not the single-path legacy layout.
func (c *RustfsAdmin) DecommissionStatuses(ctx context.Context) ([]PoolDecommissionStatus, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "decommission/status",
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureError(ctx, err, FeaturePoolDecommission)
	}
	defer drainClose(resp)

	var status decommissionStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}
	return status.Pools, nil
}

// StartPoolDecommission starts draining the pool identified by its cmdline
// through POST /rustfs/admin/v3/pools/decommission.
//
// The request answers 200 with an empty body. Once accepted the drain runs in
// the background; callers read its progress from DecommissionStatuses.
func (c *RustfsAdmin) StartPoolDecommission(ctx context.Context, pool string) error {
	return c.poolDecommissionMutation(ctx, "pools/decommission", pool)
}

// CancelPoolDecommission cancels an in-flight or queued drain through
// POST /rustfs/admin/v3/pools/cancel. It is not valid for a terminal
// (complete, failed or canceled) drain: RustFS answers 400 InvalidRequest.
func (c *RustfsAdmin) CancelPoolDecommission(ctx context.Context, pool string) error {
	return c.poolDecommissionMutation(ctx, "pools/cancel", pool)
}

// ClearPoolDecommission wipes the decommission metadata of a failed or
// canceled drain through POST /rustfs/admin/v3/pools/clear, returning the pool
// to status "none". Already moved data is not rolled back, and RustFS rejects
// the call for a completed drain.
func (c *RustfsAdmin) ClearPoolDecommission(ctx context.Context, pool string) error {
	return c.poolDecommissionMutation(ctx, "pools/clear", pool)
}

// poolDecommissionMutation issues one of the three pool decommission
// mutations. All of them identify the pool by its cmdline and accept the same
// query shape.
func (c *RustfsAdmin) poolDecommissionMutation(ctx context.Context, relPath, pool string) error {
	reqData := RequestData{
		Method:      "POST",
		RelPath:     relPath,
		QueryValues: url.Values{"pool": []string{pool}},
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return c.featureError(ctx, err, FeaturePoolDecommission)
	}
	defer drainClose(resp)
	return nil
}

func init() {
	registerFeature(FeaturePoolDecommission, featureSpec{
		route: "POST /rustfs/admin/v3/pools/decommission",
		// Probed live: 1.0.1, 1.0.2 and 1.0.3 accept the start, cancel and
		// clear routes, and serve the status route, on a deployment with two
		// pools. All three answer 501 NotImplemented for every decommission
		// route on a single-pool legacy deployment, which is a topology
		// limitation rather than a version gate.
		minVersion: &Version{Major: 1, Minor: 0, Patch: 1, Raw: "1.0.1"},
	})
}
