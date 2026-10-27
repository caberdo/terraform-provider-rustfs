// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

// newTestAdminServer spins up an httptest server speaking the RustFS admin
// API and returns a RustfsAdmin client pointed at it.
func newTestAdminServer(t *testing.T, handler http.HandlerFunc) *rustfs.RustfsAdmin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
	return &c
}

// A public admin method must hand the caller's context to the request, so a
// cancelled Terraform operation stops a blocked call instead of waiting for the
// HTTP client timeout.
func TestAdminMethodHonoursCallerContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := client.GetKmsStatus(ctx)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("GetKmsStatus with a cancelled context returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an admin call ignored the caller's cancelled context")
	}
}

// Endpoint reports the configured value before the scheme is filled in; the
// site-replication repair interlock compares its confirm attribute against it.
func TestRustfsAdminEndpoint(t *testing.T) {
	client := rustfs.New(&rustfs.RustfsAdminConfig{Endpoint: "rustfs.example.com:9000", Ssl: true})
	if got := client.Endpoint(); got != "rustfs.example.com:9000" {
		t.Fatalf("Endpoint() = %q, want the configured value", got)
	}
}
