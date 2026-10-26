// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestResolveEndpoint covers every accepted endpoint form plus the inputs that
// must be rejected. The scheme is resolved once here, so both API clients are
// built from the same origin.
func TestResolveEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		endpoint     string
		ssl          types.Bool
		wantHostPort string
		wantSecure   bool
		wantErr      string
	}{
		{
			name:         "host only keeps the plain http default",
			endpoint:     "rustfs.example.com",
			ssl:          types.BoolNull(),
			wantHostPort: "rustfs.example.com",
		},
		{
			name:         "host and port follow ssl",
			endpoint:     "rustfs.example.com:9001",
			ssl:          types.BoolValue(true),
			wantHostPort: "rustfs.example.com:9001",
			wantSecure:   true,
		},
		{
			name:         "explicit https scheme without ssl",
			endpoint:     "https://rustfs.example.com",
			ssl:          types.BoolNull(),
			wantHostPort: "rustfs.example.com",
			wantSecure:   true,
		},
		{
			name:         "explicit http scheme without ssl",
			endpoint:     "http://rustfs.example.com:9000",
			ssl:          types.BoolNull(),
			wantHostPort: "rustfs.example.com:9000",
		},
		{
			name:         "uppercase scheme is accepted",
			endpoint:     "HTTPS://Rustfs.example.com:9001",
			ssl:          types.BoolNull(),
			wantHostPort: "Rustfs.example.com:9001",
			wantSecure:   true,
		},
		{
			name:         "scheme and ssl agree",
			endpoint:     "https://rustfs.example.com",
			ssl:          types.BoolValue(true),
			wantHostPort: "rustfs.example.com",
			wantSecure:   true,
		},
		{
			name:     "scheme contradicts ssl",
			endpoint: "https://rustfs.example.com",
			ssl:      types.BoolValue(false),
			wantErr:  `specifies scheme "https" but ssl = false`,
		},
		{
			name:     "explicit http contradicts ssl",
			endpoint: "http://rustfs.example.com",
			ssl:      types.BoolValue(true),
			wantErr:  `specifies scheme "http" but ssl = true`,
		},
		{
			name:     "unknown scheme is rejected",
			endpoint: "ftp://rustfs.example.com",
			ssl:      types.BoolNull(),
			wantErr:  `scheme "ftp" is not supported`,
		},
		{
			name:         "trailing slash is tolerated",
			endpoint:     "https://rustfs.example.com:9001/",
			ssl:          types.BoolNull(),
			wantHostPort: "rustfs.example.com:9001",
			wantSecure:   true,
		},
		{
			name:     "path is rejected",
			endpoint: "https://rustfs.example.com:9001/admin",
			ssl:      types.BoolNull(),
			wantErr:  "only host or host:port is accepted, without a path, query or fragment",
		},
		{
			name:     "query is rejected",
			endpoint: "rustfs.example.com:9001?region=eu",
			ssl:      types.BoolNull(),
			wantErr:  "only host or host:port is accepted, without a path, query or fragment",
		},
		{
			name:     "fragment is rejected",
			endpoint: "rustfs.example.com:9001#admin",
			ssl:      types.BoolNull(),
			wantErr:  "only host or host:port is accepted, without a path, query or fragment",
		},
		{
			name:     "inline credentials are rejected",
			endpoint: "user:pass@rustfs.example.com:9001",
			ssl:      types.BoolNull(),
			wantErr:  "credentials belong in access_key and secret_key",
		},
		{
			name:     "invalid port is rejected",
			endpoint: "rustfs.example.com:notaport",
			ssl:      types.BoolNull(),
			wantErr:  "invalid port",
		},
		{
			name:     "empty endpoint is rejected",
			endpoint: "  ",
			ssl:      types.BoolNull(),
			wantErr:  "the endpoint is empty",
		},
		{
			name:     "a scheme with no host is rejected",
			endpoint: "https://",
			ssl:      types.BoolNull(),
			wantErr:  "only host or host:port is accepted",
		},
		{
			name:     "an empty scheme is rejected",
			endpoint: "://rustfs.example.com",
			ssl:      types.BoolNull(),
			wantErr:  `scheme "" is not supported`,
		},
		{
			name:         "ipv6 literal without a port keeps its brackets",
			endpoint:     "[::1]",
			ssl:          types.BoolNull(),
			wantHostPort: "[::1]",
		},
		{
			name:         "ipv6 literal keeps its brackets",
			endpoint:     "[::1]:9000",
			ssl:          types.BoolNull(),
			wantHostPort: "[::1]:9000",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveEndpoint(tc.endpoint, tc.ssl)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got endpoint %+v", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := endpointResolution{HostPort: tc.wantHostPort, Secure: tc.wantSecure}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("resolveEndpoint(%q, %v) = %+v, want %+v", tc.endpoint, tc.ssl, got, want)
			}
		})
	}
}

// TestEndpointResolutionRootURL pins the composition both clients use,
// including the default-port normalization a user copying a URL expects.
func TestEndpointResolutionRootURL(t *testing.T) {
	cases := []struct {
		resolution endpointResolution
		want       string
	}{
		{endpointResolution{HostPort: "rustfs.example.com:9001"}, "http://rustfs.example.com:9001"},
		{endpointResolution{HostPort: "rustfs.example.com:443", Secure: true}, "https://rustfs.example.com"},
		{endpointResolution{HostPort: "rustfs.example.com:80"}, "http://rustfs.example.com"},
		{endpointResolution{HostPort: "[::1]:9000", Secure: true}, "https://[::1]:9000"},
	}

	for _, tc := range cases {
		if got := tc.resolution.RootURL(); got != tc.want {
			t.Errorf("RootURL(%+v) = %q, want %q", tc.resolution, got, tc.want)
		}
	}
}
