// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"errors"
	"strings"
)

// featureErrorPreservingCode keeps a route's semantic error codes intact and
// only turns a bare 404/405/501 (or NotImplemented) into the server-capability
// diagnostic.
//
// featureError alone treats every 404 as a missing route, so a semantic code
// such as NoSuchBucket would be reported as "upgrade RustFS" instead of the
// real cause. This mirrors odmError in on_demand_migration.go and is shared by
// the batch-job and replication-diagnostic routes.
func (c *RustfsAdmin) featureErrorPreservingCode(ctx context.Context, err error, feature Feature) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Code != "" && !strings.EqualFold(apiErr.Code, "NotImplemented") {
			return err
		}
	}
	return c.featureError(ctx, err, feature)
}
