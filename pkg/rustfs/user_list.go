// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
)

// UserInfo is one IAM user as list-users reports it.
type UserInfo struct {
	AccessKey string `json:"accessKey"`
	Status    string `json:"status"`
	Policy    string `json:"policyName"`
}

// ListUsers returns the IAM users, optionally scoped to one bucket. The admin
// API answers GET /rustfs/admin/v3/list-users with an object keyed by access
// key — {"alice":{"status":"enabled","policyName":"readwrite"}} — and an empty
// object when no user matches, so the access key comes from the key. The
// result is sorted by access key so a data source's list is stable.
func (c *RustfsAdmin) ListUsers(ctx context.Context, bucket string) ([]UserInfo, error) {
	query := url.Values{}
	if bucket != "" {
		query.Set("bucket", bucket)
	}
	reqData := RequestData{
		Method:      "GET",
		RelPath:     "list-users",
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	var body map[string]UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	users := make([]UserInfo, 0, len(body))
	for accessKey, info := range body {
		info.AccessKey = accessKey
		users = append(users, info)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].AccessKey < users[j].AccessKey })
	return users, nil
}
