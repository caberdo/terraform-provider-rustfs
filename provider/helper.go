// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"net/http"
	"os"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func envOrDefault(envKey, defaultValue string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultValue
}

// secretKey returns the configured secret key, preferring the non-deprecated
// secret_key attribute over the legacy access_secret alias.
func (m RustfsProviderModel) secretKey() string {
	if v := m.SecretKey.ValueString(); v != "" {
		return v
	}
	return m.AccessSecret.ValueString()
}

// generateRustClientConfig builds the admin client configuration from the
// already-resolved endpoint: hostPort is scheme-less and ssl is the effective
// transport, so the admin client composes exactly the origin the S3 client was
// given.
func generateRustClientConfig(hostPort string, ssl bool, model RustfsProviderModel, httpClient *http.Client) *rustfs.RustfsAdminConfig {
	config := &rustfs.RustfsAdminConfig{
		Endpoint:     hostPort,
		AccessKey:    envOrDefault("RUSTFS_USER", model.AccessKey.ValueString()),
		AccessSecret: envOrDefault("RUSTFS_SECRET", model.secretKey()),
		Ssl:          ssl,
		HTTPClient:   httpClient,
	}
	return config
}

func stringFromPtr(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}
