// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"io"
)

func init() {
	registerFeature(FeatureMetrics, featureSpec{
		route: "GET /rustfs/admin/v3/metrics",
		// No release implements the route yet: RustFS registers it and answers
		// 501 NotImplemented (observed on 1.0.1, 1.0.2 and 1.0.3).
	})
}

// GetMetrics returns the raw metrics stream from GET /v3/metrics.
//
// RustFS registers the route but answers 501 NotImplemented on released servers
// (1.0.1 through 1.0.3); that answer is returned as an
// *UnsupportedFeatureError naming the route and the server version instead of
// the raw error payload.
func (c *RustfsAdmin) GetMetrics(ctx context.Context) (string, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "metrics",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return "", c.featureError(ctx, err, FeatureMetrics)
	}
	defer drainClose(resp)
	raw, err := io.ReadAll(resp.Body)
	return string(raw), err
}
