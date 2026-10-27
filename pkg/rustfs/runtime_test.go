// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

const runtimeCapabilitiesPayload = `{"summary":{"topology":{"state":"supported"}},` +
	`"storage_classes":{"contract_version":1,"supported_write_classes":["STANDARD","REDUCED_REDUNDANCY"],` +
	`"unsupported_write_error":"InvalidStorageClass","legacy_label_behavior":"normalized_to_effective_class"}}`

func TestGetRuntimeCapabilities(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rustfs/admin/v4/runtime/capabilities" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(runtimeCapabilitiesPayload))
	})

	capabilities, err := client.GetRuntimeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("GetRuntimeCapabilities: %v", err)
	}
	if capabilities.StorageClasses.ContractVersion != 1 {
		t.Errorf("contract_version = %d", capabilities.StorageClasses.ContractVersion)
	}
	if got := strings.Join(capabilities.StorageClasses.SupportedWriteClasses, ","); got != "STANDARD,REDUCED_REDUNDANCY" {
		t.Errorf("supported_write_classes = %q", got)
	}
	if capabilities.StorageClasses.UnsupportedWriteError != "InvalidStorageClass" {
		t.Errorf("unsupported_write_error = %q", capabilities.StorageClasses.UnsupportedWriteError)
	}
	if capabilities.StorageClasses.LegacyLabelBehavior != "normalized_to_effective_class" {
		t.Errorf("legacy_label_behavior = %q", capabilities.StorageClasses.LegacyLabelBehavior)
	}
	if capabilities.RawJSON != runtimeCapabilitiesPayload {
		t.Error("RawJSON does not carry the server body")
	}
}

func TestGetRuntimeCapabilitiesDecodeError(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"storage_classes":"not-an-object"}`))
	})

	_, err := client.GetRuntimeCapabilities(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode runtime capabilities") {
		t.Fatalf("expected a decode error, got %v", err)
	}
}

func TestGetRuntimeCapabilitiesRequiresContract(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"absent storage_classes", `{"summary":{}}`, "did not advertise the storage_classes contract"},
		{"null storage_classes", `{"storage_classes":null}`, "did not advertise the storage_classes contract"},
		{"missing contract_version", `{"storage_classes":{"supported_write_classes":["STANDARD"]}}`, "contract_version is missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.payload))
			})

			_, err := client.GetRuntimeCapabilities(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected an error containing %q, got %v", tt.want, err)
			}
		})
	}
}

// TestV4OpaqueEndpointsRejectNonJSON proves a 200 HTML body does not reach
// Terraform state while leaving the payload schema opaque.
func TestV4OpaqueEndpointsRejectNonJSON(t *testing.T) {
	tests := []struct {
		name string
		call func(*rustfs.RustfsAdmin) (string, error)
	}{
		{"plugin catalog", func(c *rustfs.RustfsAdmin) (string, error) { return c.GetPluginCatalog(context.Background()) }},
		{"plugin instances", func(c *rustfs.RustfsAdmin) (string, error) { return c.ListPluginInstances(context.Background()) }},
		{"extension catalog", func(c *rustfs.RustfsAdmin) (string, error) { return c.GetExtensionCatalog(context.Background()) }},
		{"extension instances", func(c *rustfs.RustfsAdmin) (string, error) { return c.ListExtensionInstances(context.Background()) }},
		{"cluster snapshot", func(c *rustfs.RustfsAdmin) (string, error) { return c.GetClusterSnapshot(context.Background()) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`<html>not the admin API</html>`))
			})

			_, err := tt.call(client)
			if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
				t.Fatalf("expected a JSON validation error, got %v", err)
			}
			if rustfs.IsUnsupportedFeature(err) {
				t.Errorf("a malformed 200 body must not be classified as a capability gap")
			}
		})
	}
}

// TestV4OpaqueEndpoints covers the five read-mostly v4 routes whose payloads
// the provider keeps as the server's JSON.
func TestV4OpaqueEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		relPath string
		call    func(*rustfs.RustfsAdmin) (string, error)
	}{
		{"plugin catalog", "plugins/catalog", func(c *rustfs.RustfsAdmin) (string, error) { return c.GetPluginCatalog(context.Background()) }},
		{"plugin instances", "plugins/instances", func(c *rustfs.RustfsAdmin) (string, error) { return c.ListPluginInstances(context.Background()) }},
		{"extension catalog", "extensions/catalog", func(c *rustfs.RustfsAdmin) (string, error) { return c.GetExtensionCatalog(context.Background()) }},
		{"extension instances", "extensions/instances", func(c *rustfs.RustfsAdmin) (string, error) { return c.ListExtensionInstances(context.Background()) }},
		{"cluster snapshot", "cluster/snapshot", func(c *rustfs.RustfsAdmin) (string, error) { return c.GetClusterSnapshot(context.Background()) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const payload = `{"opaque":true}`
			client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/rustfs/admin/v4/"+tt.relPath {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(payload))
			})

			got, err := tt.call(client)
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if got != payload {
				t.Errorf("%s body = %q, want the server payload", tt.name, got)
			}
		})
	}
}

// TestV4UpgradeRequiredIsNegotiationSignal proves an unmatched v4 path (511,
// empty body) becomes the typed negotiation signal, not a raw APIError.
func TestV4UpgradeRequiredIsNegotiationSignal(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUpgradeRequired)
	})

	_, err := client.GetRuntimeCapabilities(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !rustfs.IsUpgradeRequired(err) {
		t.Errorf("IsUpgradeRequired = false for %v", err)
	}

	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if !unsupported.UpgradeRequired {
		t.Error("UpgradeRequired flag is false")
	}
	message := unsupported.Error()
	for _, want := range []string{
		"does not serve GET /rustfs/admin/v4/runtime/capabilities",
		"upgrade RustFS or use the v3 route",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "status 511: :") {
		t.Errorf("the raw empty-body error leaked into the message: %q", message)
	}
}

// TestV4NotImplementedIsNotUpgradeRequired keeps the two diagnostics apart: a
// 501 means the route is not implemented at all.
func TestV4NotImplementedIsNotUpgradeRequired(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`))
	})

	_, err := client.GetClusterSnapshot(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if rustfs.IsUpgradeRequired(err) {
		t.Error("a 501 must not report as Upgrade Required")
	}
	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.UpgradeRequired {
		t.Error("UpgradeRequired flag is true for a 501")
	}
	if !strings.Contains(unsupported.Error(), "does not implement GET /rustfs/admin/v4/cluster/snapshot") {
		t.Errorf("message = %q", unsupported.Error())
	}
}

func TestV4UnexpectedErrorStaysPlain(t *testing.T) {
	client := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<Error><Code>InternalError</Code><Message>boom</Message></Error>`))
	})

	_, err := client.GetPluginCatalog(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if rustfs.IsUpgradeRequired(err) || rustfs.IsUnsupportedFeature(err) {
		t.Errorf("a server error must not be classified as a capability gap: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not carry the server payload", err)
	}
}
