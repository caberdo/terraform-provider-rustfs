// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
)

// PoolInfo describes one storage pool. The admin API identifies a pool by the
// command line it was started with (its endpoint list), which is the name the
// provider exposes.
type PoolInfo struct {
	Name   string `json:"cmdline"`
	Status string `json:"status"`
}

// ListPools returns the cluster's storage pools from
// GET /rustfs/admin/v3/pools/list. The older MinIO-style `list-pools` path is
// not registered on RustFS 1.0.x: the S3 fallback answers it with
// 501 NotImplemented.
func (c *RustfsAdmin) ListPools(ctx context.Context) ([]PoolInfo, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "pools/list",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	var pools []PoolInfo
	err = json.NewDecoder(resp.Body).Decode(&pools)
	return pools, err
}
