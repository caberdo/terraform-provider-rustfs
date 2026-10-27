// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The provider hands the admin client its own *http.Client so that both API
// clients share one transport (proxy settings, timeouts, TLS policy including
// the provider's `insecure` attribute). This asserts the client really uses it
// by talking to a TLS server whose certificate no system root can verify.
func TestNewUsesTheConfiguredHTTPClient(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":0,"cmdline":"/data","status":"active"}]`))
	}))
	defer server.Close()

	endpoint := strings.TrimPrefix(server.URL, "https://")
	insecure := &http.Client{Transport: &http.Transport{
		// #nosec G402 -- test-only client for the self-signed httptest server.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	client := New(&RustfsAdminConfig{Endpoint: endpoint, Ssl: true, HTTPClient: insecure})
	pools, err := client.ListPools(context.Background())
	if err != nil {
		t.Fatalf("admin request through the configured client failed: %v", err)
	}
	if len(pools) != 1 || pools[0].Name != "/data" {
		t.Fatalf("unexpected pools: %+v", pools)
	}

	// Same call, default client: certificate verification must reject the
	// self-signed server instead of silently succeeding.
	strictClient := New(&RustfsAdminConfig{Endpoint: endpoint, Ssl: true})
	if _, err := strictClient.ListPools(context.Background()); err == nil {
		t.Fatal("expected the default client to reject the self-signed test server")
	}
}
