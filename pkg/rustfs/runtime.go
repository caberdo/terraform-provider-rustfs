// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// The v4 admin API is additive: every route here also has its v3 neighbours
// untouched, and RustFS 1.0.3 is the first release that serves the namespace.
// A server that predates it answers 511 Upgrade Required with an empty body for
// an unmatched v4 path, which featureError turns into a negotiation
// diagnostic instead of a raw APIError.

const (
	// FeatureRuntimeCapabilities is the v4 runtime capability advertisement
	// (GET /rustfs/admin/v4/runtime/capabilities).
	FeatureRuntimeCapabilities Feature = "runtime capabilities"

	// FeaturePluginCatalog is the v4 plugin catalog
	// (GET /rustfs/admin/v4/plugins/catalog).
	FeaturePluginCatalog Feature = "plugin catalog"

	// FeaturePluginInstances is the v4 plugin instance list
	// (GET /rustfs/admin/v4/plugins/instances).
	FeaturePluginInstances Feature = "plugin instances"

	// FeatureExtensionCatalog is the v4 extension catalog
	// (GET /rustfs/admin/v4/extensions/catalog).
	FeatureExtensionCatalog Feature = "extension catalog"

	// FeatureExtensionInstances is the v4 extension instance list
	// (GET /rustfs/admin/v4/extensions/instances).
	FeatureExtensionInstances Feature = "extension instances"

	// FeatureClusterSnapshot is the v4 read-only cluster snapshot
	// (GET /rustfs/admin/v4/cluster/snapshot).
	FeatureClusterSnapshot Feature = "cluster snapshot"
)

// StorageClassCapabilities is the versioned storage-class write contract the
// runtime capability route publishes. ContractVersion is the field a gating
// caller checks; the write classes are the values the server accepts on PUT
// and COPY.
type StorageClassCapabilities struct {
	ContractVersion       uint32   `json:"contract_version"`
	SupportedWriteClasses []string `json:"supported_write_classes"`
	UnsupportedWriteError string   `json:"unsupported_write_error"`
	LegacyLabelBehavior   string   `json:"legacy_label_behavior"`
}

// RuntimeCapabilities is what GET /v4/runtime/capabilities advertises. The
// provider models the storage-class contract because callers gate writes on
// it; the rest of the payload stays opaque in RawJSON so a newer server can add
// fields without a provider release.
type RuntimeCapabilities struct {
	StorageClasses StorageClassCapabilities `json:"storage_classes"`

	RawJSON string `json:"-"`
}

// adminV4Get issues one GET against the v4 admin namespace and returns the
// body. A 511/501 answer is converted into an *UnsupportedFeatureError naming
// the route, the API version and the server version.
func (c *RustfsAdmin) adminV4Get(ctx context.Context, relPath string, feature Feature) ([]byte, error) {
	reqData := RequestData{
		Method:  http.MethodGet,
		RelPath: relPath,
		Version: adminVersionV4,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureError(ctx, err, feature)
	}
	defer drainClose(resp)

	return io.ReadAll(resp.Body)
}

// GetRuntimeCapabilities reads GET /v4/runtime/capabilities, including the
// storage-class contract. RawJSON keeps the full payload for fields the
// provider does not model yet. A 200 response that omits the contract (or its
// version) is an error: reporting a zero-valued contract would make a gating
// expression silently evaluate false.
func (c *RustfsAdmin) GetRuntimeCapabilities(ctx context.Context) (RuntimeCapabilities, error) {
	raw, err := c.adminV4Get(ctx, "runtime/capabilities", FeatureRuntimeCapabilities)
	if err != nil {
		return RuntimeCapabilities{}, err
	}

	var presence struct {
		StorageClasses json.RawMessage `json:"storage_classes"`
	}
	if err := json.Unmarshal(raw, &presence); err != nil {
		return RuntimeCapabilities{}, fmt.Errorf("decode runtime capabilities: %w", err)
	}
	if len(presence.StorageClasses) == 0 || string(presence.StorageClasses) == "null" {
		return RuntimeCapabilities{}, fmt.Errorf(
			"decode runtime capabilities: the server did not advertise the storage_classes contract",
		)
	}

	result := RuntimeCapabilities{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return RuntimeCapabilities{}, fmt.Errorf("decode runtime capabilities: %w", err)
	}
	if result.StorageClasses.ContractVersion == 0 {
		return RuntimeCapabilities{}, fmt.Errorf(
			"decode runtime capabilities: the storage_classes contract_version is missing",
		)
	}
	result.RawJSON = string(raw)
	return result, nil
}

// validateOpaqueV4JSON rejects a 200 body that is not JSON without imposing a
// schema on it: the payload stays opaque, but an HTML error page from a proxy
// surfaces here instead of as a jsondecode failure in the user's configuration.
func validateOpaqueV4JSON(raw []byte, what string) error {
	if !json.Valid(raw) {
		return fmt.Errorf("decode %s: the server returned a body that is not valid JSON", what)
	}
	return nil
}

// GetPluginCatalog reads GET /v4/plugins/catalog. The payload is returned as
// the server's JSON verbatim: the v4 plugin schema is new and the provider does
// not invent a Terraform shape for it.
func (c *RustfsAdmin) GetPluginCatalog(ctx context.Context) (string, error) {
	raw, err := c.adminV4Get(ctx, "plugins/catalog", FeaturePluginCatalog)
	if err != nil {
		return "", err
	}
	if err := validateOpaqueV4JSON(raw, "plugin catalog"); err != nil {
		return "", err
	}
	return string(raw), nil
}

// ListPluginInstances reads GET /v4/plugins/instances. Secrets the server
// reports are already redacted in the payload.
func (c *RustfsAdmin) ListPluginInstances(ctx context.Context) (string, error) {
	raw, err := c.adminV4Get(ctx, "plugins/instances", FeaturePluginInstances)
	if err != nil {
		return "", err
	}
	if err := validateOpaqueV4JSON(raw, "plugin instances"); err != nil {
		return "", err
	}
	return string(raw), nil
}

// GetExtensionCatalog reads GET /v4/extensions/catalog, returned as raw JSON.
func (c *RustfsAdmin) GetExtensionCatalog(ctx context.Context) (string, error) {
	raw, err := c.adminV4Get(ctx, "extensions/catalog", FeatureExtensionCatalog)
	if err != nil {
		return "", err
	}
	if err := validateOpaqueV4JSON(raw, "extension catalog"); err != nil {
		return "", err
	}
	return string(raw), nil
}

// ListExtensionInstances reads GET /v4/extensions/instances, returned as raw
// JSON.
func (c *RustfsAdmin) ListExtensionInstances(ctx context.Context) (string, error) {
	raw, err := c.adminV4Get(ctx, "extensions/instances", FeatureExtensionInstances)
	if err != nil {
		return "", err
	}
	if err := validateOpaqueV4JSON(raw, "extension instances"); err != nil {
		return "", err
	}
	return string(raw), nil
}

// GetClusterSnapshot reads GET /v4/cluster/snapshot. The snapshot is a large,
// deployment-shaped payload, so it stays raw JSON.
func (c *RustfsAdmin) GetClusterSnapshot(ctx context.Context) (string, error) {
	raw, err := c.adminV4Get(ctx, "cluster/snapshot", FeatureClusterSnapshot)
	if err != nil {
		return "", err
	}
	if err := validateOpaqueV4JSON(raw, "cluster snapshot"); err != nil {
		return "", err
	}
	return string(raw), nil
}

func init() {
	// RustFS 1.0.3 is the first release with the v4 namespace; on an older
	// server an unmatched v4 path answers 511 Upgrade Required, which the
	// feature error reports as "upgrade RustFS or use the v3 route".
	minV4 := &Version{Major: 1, Minor: 0, Patch: 3, Raw: "1.0.3"}

	registerFeature(FeatureRuntimeCapabilities, featureSpec{
		route:      "GET /rustfs/admin/v4/runtime/capabilities",
		version:    adminVersionV4,
		minVersion: minV4,
	})
	registerFeature(FeaturePluginCatalog, featureSpec{
		route:      "GET /rustfs/admin/v4/plugins/catalog",
		version:    adminVersionV4,
		minVersion: minV4,
	})
	registerFeature(FeaturePluginInstances, featureSpec{
		route:      "GET /rustfs/admin/v4/plugins/instances",
		version:    adminVersionV4,
		minVersion: minV4,
	})
	registerFeature(FeatureExtensionCatalog, featureSpec{
		route:      "GET /rustfs/admin/v4/extensions/catalog",
		version:    adminVersionV4,
		minVersion: minV4,
	})
	registerFeature(FeatureExtensionInstances, featureSpec{
		route:      "GET /rustfs/admin/v4/extensions/instances",
		version:    adminVersionV4,
		minVersion: minV4,
	})
	registerFeature(FeatureClusterSnapshot, featureSpec{
		route:      "GET /rustfs/admin/v4/cluster/snapshot",
		version:    adminVersionV4,
		minVersion: minV4,
	})
}
