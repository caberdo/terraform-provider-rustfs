// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Version is a parsed RustFS server version.
//
// RustFS does not report one canonical format: GET /v3/healthinfo answers
// e.g. "refs/tags/1.0.0-rc.5" while GET /v3/info answers
// "2026-09-01T17:35:49Z@1.0.0-rc.5". Raw keeps the string exactly as the
// server reported it so diagnostics can quote the server verbatim.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
	Raw        string
}

// ParseServerVersion parses the version strings RustFS reports. The last
// '@'-separated segment wins (GET /v3/info prefixes a build timestamp) and a
// "refs/tags/" or "v" prefix is stripped.
func ParseServerVersion(raw string) (Version, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Version{}, errors.New("empty server version")
	}

	candidate := trimmed
	if at := strings.LastIndex(candidate, "@"); at >= 0 {
		candidate = candidate[at+1:]
	}
	candidate = strings.TrimPrefix(candidate, "refs/tags/")
	candidate = strings.TrimPrefix(candidate, "v")

	core, prerelease := candidate, ""
	if i := strings.IndexByte(candidate, '-'); i >= 0 {
		core, prerelease = candidate[:i], candidate[i+1:]
	}
	// Build metadata ("+...") carries no ordering information.
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	if i := strings.IndexByte(prerelease, '+'); i >= 0 {
		prerelease = prerelease[:i]
	}

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("unparseable server version %q: expected MAJOR.MINOR.PATCH", raw)
	}
	numbers := make([]int, 3)
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return Version{}, fmt.Errorf("unparseable server version %q: %q is not a number", raw, part)
		}
		numbers[i] = number
	}

	return Version{
		Major:      numbers[0],
		Minor:      numbers[1],
		Patch:      numbers[2],
		Prerelease: prerelease,
		Raw:        trimmed,
	}, nil
}

// Compare returns -1, 0 or 1 when v is older than, equal to or newer than
// other. A prerelease sorts before the matching release (1.0.0-rc.5 < 1.0.0),
// which is the precedence rule capability gating relies on.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
	} {
		switch {
		case pair[0] < pair[1]:
			return -1
		case pair[0] > pair[1]:
			return 1
		}
	}

	switch {
	case v.Prerelease == other.Prerelease:
		return 0
	case v.Prerelease == "":
		return 1
	case other.Prerelease == "":
		return -1
	default:
		return strings.Compare(v.Prerelease, other.Prerelease)
	}
}

// AtLeast reports whether v is other or newer.
func (v Version) AtLeast(other Version) bool {
	return v.Compare(other) >= 0
}

// String returns the dotted version, including the prerelease when present.
func (v Version) String() string {
	version := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		version += "-" + v.Prerelease
	}
	return version
}

// Feature names a RustFS server capability the provider depends on.
type Feature string

const (
	// FeatureMetrics is the admin metrics stream read by the rustfs_metrics
	// data source (GET /rustfs/admin/v3/metrics).
	FeatureMetrics Feature = "metrics"

	// FeatureTableBucket is the disable half of the S3 Tables catalog switch
	// (DELETE /iceberg/v1/buckets/{bucket}). Enable and read work on every
	// supported release; only the disable route is missing on 1.0.1.
	FeatureTableBucket Feature = "the S3 Tables catalog switch"
)

// featureSpec records what a feature needs from the server.
type featureSpec struct {
	// route is the admin API route that serves the feature.
	route string
	// version is the admin API version the route lives on ("v3" or "v4").
	// Empty means defaultAdminAPIVersion, so existing registrations are v3.
	version string
	// minVersion is the first RustFS release that implements the route. nil
	// means no released server implements it yet.
	minVersion *Version
}

// apiVersion returns the admin API version the route lives on.
func (s featureSpec) apiVersion() string {
	if s.version == "" {
		return defaultAdminAPIVersion
	}
	return s.version
}

// featureSpecs is the capability matrix the provider reasons about. It is
// populated by registerFeature from the file that owns each feature, so a
// feature branch adds one file instead of editing a shared literal.
var (
	featureMu    sync.RWMutex
	featureSpecs = make(map[Feature]featureSpec)
)

// registerFeature records what a feature needs from the server. Call it from
// init in the file that owns the feature.
func registerFeature(feature Feature, spec featureSpec) {
	featureMu.Lock()
	defer featureMu.Unlock()

	if _, exists := featureSpecs[feature]; exists {
		panic("rustfs: feature " + string(feature) + " is registered twice")
	}
	featureSpecs[feature] = spec
}

// featureSpecFor returns the spec registered for a feature. An unregistered
// feature is a programming error: silently returning the zero spec would hide a
// missing registration.
func featureSpecFor(feature Feature) featureSpec {
	spec, ok := lookupFeatureSpec(feature)
	if !ok {
		panic("rustfs: feature " + string(feature) + " is not registered; add registerFeature to the file that owns it")
	}
	return spec
}

// lookupFeatureSpec returns the registered spec and whether one exists. Unlike
// featureSpecFor it is safe on unregistered features and is used where a panic
// would be inappropriate, such as error formatting.
func lookupFeatureSpec(feature Feature) (featureSpec, bool) {
	featureMu.RLock()
	defer featureMu.RUnlock()

	spec, ok := featureSpecs[feature]
	return spec, ok
}

// Route returns the admin API route that serves the feature. It panics when
// the feature was never registered, because that is a programming error rather
// than an absent capability.
func (f Feature) Route() string {
	return featureSpecFor(f).route
}

// UnsupportedFeatureError reports that the server does not implement the route
// a provider capability needs.
//
// RustFS registers admin routes before it implements them and answers
// 501 NotImplemented instead of 404, so route presence in the server's route
// table is not proof of support. The error carries the feature, its route and
// the version the server reported instead of the raw XML payload.
type UnsupportedFeatureError struct {
	Feature Feature
	Route   string
	// APIVersion is the admin API version the route lives on ("v3" or "v4").
	APIVersion string
	// UpgradeRequired is true when the server answered 511 Upgrade Required:
	// the v4 namespace exists, but this particular path is not served. It is
	// deliberately distinct from a 501, where the route is not implemented at
	// all.
	UpgradeRequired bool
	// ServerVersion is the version the server reported, normalized to
	// MAJOR.MINOR.PATCH when it could be parsed. It is empty when the version
	// could not be determined.
	ServerVersion string
	MinVersion    *Version
	Err           error
}

func (e *UnsupportedFeatureError) Error() string {
	server := "the RustFS server"
	if e.ServerVersion != "" {
		server = "RustFS " + e.ServerVersion
	}

	route := e.Route
	if route == "" {
		if spec, ok := lookupFeatureSpec(e.Feature); ok {
			route = spec.route
		}
	}
	if route == "" {
		route = string(e.Feature)
	}

	version := e.APIVersion
	if version == "" {
		if spec, ok := lookupFeatureSpec(e.Feature); ok {
			version = spec.apiVersion()
		}
	}
	if version == "" {
		version = defaultAdminAPIVersion
	}

	if e.UpgradeRequired {
		return fmt.Sprintf(
			"%s does not serve %s: %s is not available on the %s admin API; upgrade RustFS or use the v3 route",
			server, route, e.Feature, version,
		)
	}

	if e.MinVersion != nil {
		return fmt.Sprintf(
			"%s does not implement %s: %s is not available on this server; it requires RustFS %s or later",
			server, route, e.Feature, e.MinVersion,
		)
	}
	return fmt.Sprintf("%s does not implement %s: %s is not available on this server", server, route, e.Feature)
}

// Unwrap exposes the underlying API error so callers can still inspect the
// response status and payload.
func (e *UnsupportedFeatureError) Unwrap() error {
	return e.Err
}

// IsUnsupportedFeature reports whether err is an *UnsupportedFeatureError.
func IsUnsupportedFeature(err error) bool {
	var unsupported *UnsupportedFeatureError
	return errors.As(err, &unsupported)
}

// ServerVersion returns the version RustFS reports on GET /v3/healthinfo, both
// parsed and exactly as reported. The raw string is returned even when it
// cannot be parsed, so diagnostics can quote the server verbatim.
func (c *RustfsAdmin) ServerVersion(ctx context.Context) (Version, string, error) {
	info, err := c.GetHealthInfo(ctx)
	if err != nil {
		return Version{}, "", err
	}

	raw := strings.TrimSpace(info.Version)
	if raw == "" {
		return Version{}, "", errors.New("the server did not report a version")
	}

	version, err := ParseServerVersion(raw)
	return version, raw, err
}

// isUnsupportedRoute reports whether err is the server's answer for a route it
// does not implement. RustFS replies 501 NotImplemented for routes it registers
// without implementing them; 404 and 405 cover routes that were never
// registered. A 404 raised by an implemented route for a missing object is only
// ever seen by callers that pass it here, which feature-gated operations do not.
func isUnsupportedRoute(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	}
	return strings.EqualFold(apiErr.Code, "NotImplemented")
}

// featureError converts the server's "route not implemented" answer into an
// *UnsupportedFeatureError carrying the feature, the route and the server
// version. A 511 Upgrade Required answer is reported as a negotiation problem
// instead: the v4 namespace exists, but the path is not served. Every other
// error is returned unchanged. The version lookup is best effort: the
// capability gap is already established without it.
func (c *RustfsAdmin) featureError(ctx context.Context, err error, feature Feature) error {
	if err == nil {
		return err
	}
	upgradeRequired := IsUpgradeRequired(err)
	if !upgradeRequired && !isUnsupportedRoute(err) {
		return err
	}

	spec := featureSpecFor(feature)
	unsupported := &UnsupportedFeatureError{
		Feature:         feature,
		Route:           spec.route,
		APIVersion:      spec.apiVersion(),
		UpgradeRequired: upgradeRequired,
		MinVersion:      spec.minVersion,
		Err:             err,
	}
	if _, raw, _ := c.ServerVersion(ctx); raw != "" {
		unsupported.ServerVersion = raw
		if version, parseErr := ParseServerVersion(raw); parseErr == nil {
			unsupported.ServerVersion = version.String()
		}
	}
	return unsupported
}
