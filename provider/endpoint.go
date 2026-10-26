// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// endpointResolution is the single normalized reading of the provider
// `endpoint` attribute. Both API clients are built from it, so they cannot
// disagree about the scheme or the port the way they used to: the S3 client
// used to keep a supplied scheme while the admin client prepended a second one
// (https://https://host).
type endpointResolution struct {
	// HostPort is the endpoint without scheme, path or default port, as in
	// "rustfs.example.com:9001" or "[::1]:9000". It is what the admin client
	// takes, so RootURL stays the only place that composes a URL.
	HostPort string
	// Secure is the effective transport: an explicit scheme wins, otherwise
	// the `ssl` attribute decides, defaulting to plain HTTP.
	Secure bool
}

// RootURL is the fully composed origin both clients dial.
func (e endpointResolution) RootURL() string {
	return rustfs.RootURL(e.HostPort, e.Secure)
}

// resolveEndpoint normalizes the configured endpoint. It accepts host,
// host:port and scheme://host[:port]; a path, query, fragment or embedded
// credentials are rejected, and a scheme that contradicts an explicitly set
// `ssl` attribute is an error naming both values rather than a silent choice.
func resolveEndpoint(raw string, ssl types.Bool) (endpointResolution, error) {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		return endpointResolution{}, fmt.Errorf("the endpoint is empty")
	}

	secure := ssl.ValueBool()
	scheme := ""
	if i := strings.Index(endpoint, "://"); i >= 0 {
		scheme = strings.ToLower(endpoint[:i])
		endpoint = endpoint[i+3:]
		switch scheme {
		case "http":
			secure = false
		case "https":
			secure = true
		default:
			return endpointResolution{}, fmt.Errorf(
				"scheme %q is not supported: use http, https, or leave the scheme out and set ssl instead", scheme)
		}
	}

	// `ssl` unset means "follow the scheme"; setting it next to a contradicting
	// scheme is a configuration mistake, not something to guess about.
	if scheme != "" && !ssl.IsNull() && !ssl.IsUnknown() && ssl.ValueBool() != secure {
		return endpointResolution{}, fmt.Errorf(
			"endpoint %q specifies scheme %q but ssl = %t; remove the scheme from the endpoint or set ssl = %t",
			raw, scheme, ssl.ValueBool(), secure)
	}

	hostPort, err := normalizeHostPort(endpoint)
	if err != nil {
		return endpointResolution{}, fmt.Errorf("endpoint %q is invalid: %w", raw, err)
	}

	return endpointResolution{HostPort: hostPort, Secure: secure}, nil
}

// normalizeHostPort validates a scheme-less endpoint and returns its
// canonical host[:port] form. A single trailing slash is tolerated because
// users copy URLs; anything beyond it would be a path and is rejected.
func normalizeHostPort(endpoint string) (string, error) {
	endpoint = strings.TrimSuffix(endpoint, "/")
	if endpoint == "" {
		return "", fmt.Errorf("only host or host:port is accepted")
	}

	u, err := url.Parse("//" + endpoint)
	if err != nil {
		return "", err
	}
	if u.User != nil {
		return "", fmt.Errorf("credentials belong in access_key and secret_key, not inside the endpoint")
	}
	if u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("only host or host:port is accepted, without a path, query or fragment")
	}

	return u.Host, nil
}
