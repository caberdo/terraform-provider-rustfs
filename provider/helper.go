package provider

import (
	"os"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
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

func generateRustClientConfig(model RustfsProviderModel) *client.RustfsAdminConfig {
	config := &client.RustfsAdminConfig{
		Endpoint:     envOrDefault("RUSTFS_ENDPOINT", model.Endpoint.ValueString()),
		AccessKey:    envOrDefault("RUSTFS_USER", model.AccessKey.ValueString()),
		AccessSecret: envOrDefault("RUSTFS_SECRET", model.secretKey()),
		Ssl:          model.Ssl.ValueBool(),
		Insecure:     model.Insecure.ValueBool(),
	}
	return config
}

func stringFromPtr(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}
