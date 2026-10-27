// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/url"
)

type UserMFAStatus struct {
	AccessKey              string `json:"access_key"`
	Enabled                bool   `json:"enabled"`
	ActivatedAt            string `json:"activated_at,omitempty"`
	RecoveryCodesRemaining int    `json:"recovery_codes_remaining"`
}

// ReadUserMFA returns the MFA status of a user.
func (c *RustfsAdmin) ReadUserMFA(ctx context.Context, accessKey string) (UserMFAStatus, error) {
	var status UserMFAStatus
	urlValues := make(url.Values)
	urlValues.Set("accessKey", accessKey)
	req_data := RequestData{
		Method:      "GET",
		RelPath:     "user/mfa",
		QueryValues: urlValues,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return status, err
	}
	defer drainClose(resp)
	err = json.NewDecoder(resp.Body).Decode(&status)
	return status, err
}

// ClearUserMFA clears the second factor of a user.
func (c *RustfsAdmin) ClearUserMFA(ctx context.Context, accessKey string) error {
	urlValues := make(url.Values)
	urlValues.Set("accessKey", accessKey)
	req_data := RequestData{
		Method:      "DELETE",
		RelPath:     "user/mfa",
		QueryValues: urlValues,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}
