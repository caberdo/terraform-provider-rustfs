// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// FeatureBucketRecovery is the generation-fenced recovery of orphaned bucket
// metadata (POST /rustfs/admin/v3/recover-orphaned-bucket/{bucket}).
//
// The route ships in RustFS 1.0.2; 1.0.1 does not register it and answers 404.
const FeatureBucketRecovery Feature = "orphaned bucket recovery"

// BucketRecoveryResult is the server's answer to a successful recovery.
//
// RetiredIncarnationID echoes the generation the caller fenced the request
// with. The server mints a new incarnation for the recovered bucket but does
// not return it, so there is no field for it here.
type BucketRecoveryResult struct {
	Bucket               string `json:"bucket"`
	RetiredIncarnationID string `json:"retiredIncarnationId"`
}

// bucketRecoveryRequest is the exact request body the handler accepts. It is
// plain camelCase JSON, and the server denies unknown fields, so the field set
// must not grow.
type bucketRecoveryRequest struct {
	ExpectedIncarnationID string `json:"expectedIncarnationId"`
}

// RecoverOrphanedBucket reconciles the metadata of a bucket whose volume was
// removed outside the storage API, using the exact incarnation UUID the
// metadata belongs to as the fence.
//
// Recovery is refused when any disk still holds the volume, when the cluster
// cannot prove the volume absent on every disk, or when the identity does not
// match. An interrupted cleanup can be retried safely with the same identity.
// The call never deletes a bucket or its objects; it retires the orphaned
// generation and drops its metadata.
func (c *RustfsAdmin) RecoverOrphanedBucket(ctx context.Context, bucket, expectedIncarnationID string) (BucketRecoveryResult, error) {
	content, err := json.Marshal(bucketRecoveryRequest{ExpectedIncarnationID: expectedIncarnationID})
	if err != nil {
		return BucketRecoveryResult{}, err
	}

	reqData := RequestData{
		Method:  http.MethodPost,
		RelPath: "recover-orphaned-bucket/" + url.PathEscape(bucket),
		Content: content,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		// RustFS 1.0.1 never registered the route, so it answers 404. Name the
		// feature, route and version instead of surfacing a bare 404.
		return BucketRecoveryResult{}, c.featureError(ctx, err, FeatureBucketRecovery)
	}
	defer drainClose(resp)

	var result BucketRecoveryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return BucketRecoveryResult{}, err
	}
	return result, nil
}

func init() {
	registerFeature(FeatureBucketRecovery, featureSpec{
		route: "POST /rustfs/admin/v3/recover-orphaned-bucket/{bucket}",
		// The handler module first appears in 1.0.2. 1.0.2 and 1.0.3 ship the
		// identical handler, and 1.0.1 has no route at all.
		minVersion: &Version{Major: 1, Minor: 0, Patch: 2, Raw: "1.0.2"},
	})
}
