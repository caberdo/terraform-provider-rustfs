// Copyright (c) 2026 the terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

// TestAccV4AdminRouteProbe records which v4 admin routes the running server
// serves (200) and which it rejects (511 on 1.0.3's v4 fallback, 501/404 on a
// server that predates the namespace). Run it once per RustFS release (1.0.1,
// 1.0.2, 1.0.3) and copy the logged matrix into the PR; it deliberately treats
// a pre-v4 answer as a recorded result, not a failure, and only rejects a
// status outside that set.
func TestAccV4AdminRouteProbe(t *testing.T) {
	testAccPreCheck(t)
	client := testAccRustClient()

	routes := []struct {
		name string
		call func() error
	}{
		{"GET /rustfs/admin/v4/runtime/capabilities", func() error { _, err := client.GetRuntimeCapabilities(context.Background()); return err }},
		{"GET /rustfs/admin/v4/plugins/catalog", func() error { _, err := client.GetPluginCatalog(context.Background()); return err }},
		{"GET /rustfs/admin/v4/plugins/instances", func() error { _, err := client.ListPluginInstances(context.Background()); return err }},
		{"GET /rustfs/admin/v4/extensions/catalog", func() error { _, err := client.GetExtensionCatalog(context.Background()); return err }},
		{"GET /rustfs/admin/v4/extensions/instances", func() error { _, err := client.ListExtensionInstances(context.Background()); return err }},
		{"GET /rustfs/admin/v4/cluster/snapshot", func() error { _, err := client.GetClusterSnapshot(context.Background()); return err }},
	}

	for _, route := range routes {
		err := route.call()
		if err == nil {
			t.Logf("%s: 200", route.name)
			continue
		}

		var apiErr *rustfs.APIError
		if !errors.As(err, &apiErr) {
			t.Errorf("%s: expected an API status, got %T: %v", route.name, err, err)
			continue
		}
		t.Logf("%s: %d", route.name, apiErr.StatusCode)
		switch apiErr.StatusCode {
		case http.StatusUpgradeRequired, http.StatusNotImplemented, http.StatusNotFound:
		default:
			t.Errorf("%s: status %d, want 200, 511, 501 or 404", route.name, apiErr.StatusCode)
		}
	}
}
