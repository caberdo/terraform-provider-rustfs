// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"fmt"
)

// NotificationTarget describes an event-notification target as returned by the
// admin list endpoint. Like audit targets, the list response does not echo the
// target configuration (endpoint, credentials, ...) — only identity and runtime
// health metadata, so those config fields must be preserved by the caller
// rather than read back.
type NotificationTarget struct {
	AccountID    string `json:"account_id"`
	Service      string `json:"service"`
	Status       string `json:"status"`
	HealthState  string `json:"health_state"`
	HealthReason string `json:"health_reason"`
	Source       string `json:"source"`
}

// notificationTargetListResponse is the envelope returned by GET target/list.
type notificationTargetListResponse struct {
	NotifyEnabled bool                 `json:"notify_enabled"`
	Endpoints     []NotificationTarget `json:"notification_endpoints"`
}

// notificationTargetSetBody is the request body for PUT target/{type}/{name}.
type notificationTargetSetBody struct {
	KeyValues []TargetKeyValue `json:"key_values"`
}

// ListNotificationTargets lists all configured event-notification targets.
func (c *RustfsAdmin) ListNotificationTargets(ctx context.Context) ([]NotificationTarget, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "target/list",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	var out notificationTargetListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Endpoints, nil
}

// SetNotificationTarget adds or edits an event-notification target, sending
// keyValues as the target configuration. The server validates the keys against
// the target type and automatically enables the target.
func (c *RustfsAdmin) SetNotificationTarget(ctx context.Context, targetType, targetName string, keyValues []TargetKeyValue) error {
	body := notificationTargetSetBody{KeyValues: keyValues}
	//#nosec G117 — the marshaled body is the target's own configuration and
	// may contain credentials (for example an amqp URL with an embedded
	// password), which is expected to be transmitted to the admin API; it is
	// never logged by this client.
	bytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	reqData := RequestData{
		Method:  "PUT",
		RelPath: fmt.Sprintf("target/%s/%s", targetType, targetName),
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}

// ResetNotificationTarget removes an event-notification target, reverting it to
// the server default (the admin API exposes no plain delete for targets).
func (c *RustfsAdmin) ResetNotificationTarget(ctx context.Context, targetType, targetName string) error {
	reqData := RequestData{
		Method:  "DELETE",
		RelPath: fmt.Sprintf("target/%s/%s/reset", targetType, targetName),
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}
