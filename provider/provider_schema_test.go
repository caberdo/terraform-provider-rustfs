// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestProviderSchemaSecretKeyAlias(t *testing.T) {
	p := &RustfsProvider{}
	var resp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	secretKey, ok := resp.Schema.Attributes["secret_key"]
	if !ok {
		t.Fatal("expected secret_key attribute in provider schema")
	}
	secretKeyAttr, ok := secretKey.(schema.StringAttribute)
	if !ok {
		t.Fatalf("expected StringAttribute for secret_key, got %T", secretKey)
	}
	if !secretKeyAttr.Sensitive {
		t.Error("secret_key should be marked sensitive")
	}

	accessSecret, ok := resp.Schema.Attributes["access_secret"]
	if !ok {
		t.Fatal("expected access_secret attribute in provider schema")
	}
	accessSecretAttr, ok := accessSecret.(schema.StringAttribute)
	if !ok {
		t.Fatalf("expected StringAttribute for access_secret, got %T", accessSecret)
	}
	if accessSecretAttr.DeprecationMessage == "" {
		t.Error("access_secret should have a deprecation message")
	}
}

func TestProviderModelSecretKeyPrecedence(t *testing.T) {
	model := RustfsProviderModel{
		SecretKey:    types.StringValue(rotatedSecretKey),
		AccessSecret: types.StringValue(legacySecretKey),
	}
	if got := model.secretKey(); got != rotatedSecretKey {
		t.Errorf("expected secret_key to take precedence, got %q", got)
	}

	model.SecretKey = types.StringNull()
	if got := model.secretKey(); got != legacySecretKey {
		t.Errorf("expected access_secret fallback, got %q", got)
	}

	model.AccessSecret = types.StringNull()
	if got := model.secretKey(); got != "" {
		t.Errorf("expected empty string when neither set, got %q", got)
	}
}

// Provider credentials for the configure cases. The values are generated (and
// overridable from the environment) so no credential literal lives in the
// source; the tests only need them to be non-empty and distinct.
var (
	configuredAccessKey = unitTestCredential("RUSTFS_TEST_ACCESS_KEY", 'c')
	configuredSecretKey = unitTestCredential("RUSTFS_TEST_SECRET_KEY", 'd')
	envAccessKey        = unitTestCredential("RUSTFS_TEST_ENV_ACCESS_KEY", 'e')
	rotatedSecretKey    = unitTestCredential("RUSTFS_TEST_ROTATED_SECRET_KEY", 'r')
	legacySecretKey     = unitTestCredential("RUSTFS_TEST_LEGACY_SECRET_KEY", 'l')
)

// providerConfigValue builds the framework configuration for the provider
// schema from model, the way Terraform hands a provider block to Configure.
func providerConfigValue(model RustfsProviderModel) (tfsdk.Config, error) {
	p := &RustfsProvider{}
	schemaResp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		return tfsdk.Config{}, fmt.Errorf("provider schema: %v", schemaResp.Diagnostics)
	}

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		return tfsdk.Config{}, fmt.Errorf("provider config set: %v", diags)
	}
	return tfsdk.Config{Schema: schemaResp.Schema, Raw: plan.Raw}, nil
}

func configureProvider(t *testing.T, model RustfsProviderModel) *provider.ConfigureResponse {
	t.Helper()

	resp, err := configureProviderValue(model)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// configureProviderValue is configureProvider without *testing.T, for callers
// such as a CheckDestroy helper that only have an error return.
func configureProviderValue(model RustfsProviderModel) (*provider.ConfigureResponse, error) {
	config, err := providerConfigValue(model)
	if err != nil {
		return nil, err
	}

	resp := &provider.ConfigureResponse{}
	New("test")().Configure(context.Background(), provider.ConfigureRequest{Config: config}, resp)
	return resp, nil
}

// TestProviderConfigure covers the endpoint/credential resolution the provider
// performs before any resource runs, including the environment precedence and
// the http/https scheme the S3 client is built with.
func TestProviderConfigure(t *testing.T) {
	cases := []struct {
		name          string
		envEndpoint   string
		envUser       string
		model         RustfsProviderModel
		wantEndpoint  string
		wantAccessKey string
	}{
		{
			name:          "configured endpoint over a plain http transport",
			model:         RustfsProviderModel{Endpoint: types.StringValue("cfghost:9001"), AccessKey: types.StringValue(configuredAccessKey), SecretKey: types.StringValue(configuredSecretKey)},
			wantEndpoint:  "http://cfghost:9001",
			wantAccessKey: configuredAccessKey,
		},
		{
			name:          "ssl selects the https transport",
			model:         RustfsProviderModel{Endpoint: types.StringValue("cfghost:9001"), AccessKey: types.StringValue(configuredAccessKey), SecretKey: types.StringValue(configuredSecretKey), Ssl: types.BoolValue(true)},
			wantEndpoint:  "https://cfghost:9001",
			wantAccessKey: configuredAccessKey,
		},
		{
			name:          "endpoint that carries its own scheme is left alone",
			model:         RustfsProviderModel{Endpoint: types.StringValue("http://cfghost:9001"), AccessKey: types.StringValue(configuredAccessKey), SecretKey: types.StringValue(configuredSecretKey)},
			wantEndpoint:  "http://cfghost:9001",
			wantAccessKey: configuredAccessKey,
		},
		{
			name:          "https scheme selects the transport without ssl",
			model:         RustfsProviderModel{Endpoint: types.StringValue("https://cfghost:9001"), AccessKey: types.StringValue(configuredAccessKey), SecretKey: types.StringValue(configuredSecretKey)},
			wantEndpoint:  "https://cfghost:9001",
			wantAccessKey: configuredAccessKey,
		},
		{
			name:          "https scheme with the default port is normalized",
			model:         RustfsProviderModel{Endpoint: types.StringValue("https://cfghost:443"), AccessKey: types.StringValue(configuredAccessKey), SecretKey: types.StringValue(configuredSecretKey)},
			wantEndpoint:  "https://cfghost",
			wantAccessKey: configuredAccessKey,
		},
		{
			name:          "https scheme with an ipv6 literal",
			model:         RustfsProviderModel{Endpoint: types.StringValue("https://[::1]:9001"), AccessKey: types.StringValue(configuredAccessKey), SecretKey: types.StringValue(configuredSecretKey)},
			wantEndpoint:  "https://[::1]:9001",
			wantAccessKey: configuredAccessKey,
		},
		{
			name:          "endpoint in the environment variable may carry a scheme",
			envEndpoint:   "https://envhost:9501",
			envUser:       envAccessKey,
			model:         RustfsProviderModel{Endpoint: types.StringValue("cfghost:9001"), AccessKey: types.StringValue(configuredAccessKey), SecretKey: types.StringValue(configuredSecretKey)},
			wantEndpoint:  "https://envhost:9501",
			wantAccessKey: envAccessKey,
		},
		{
			name:          "environment variables win over the provider block",
			envEndpoint:   "envhost:9501",
			envUser:       envAccessKey,
			model:         RustfsProviderModel{Endpoint: types.StringValue("cfghost:9001"), AccessKey: types.StringValue(configuredAccessKey), SecretKey: types.StringValue(configuredSecretKey)},
			wantEndpoint:  "http://envhost:9501",
			wantAccessKey: envAccessKey,
		},
		{
			name:          "legacy access_secret alias is accepted",
			model:         RustfsProviderModel{Endpoint: types.StringValue("cfghost:9001"), AccessKey: types.StringValue(configuredAccessKey), AccessSecret: types.StringValue(legacySecretKey)},
			wantEndpoint:  "http://cfghost:9001",
			wantAccessKey: configuredAccessKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RUSTFS_ENDPOINT", tc.envEndpoint)
			t.Setenv("RUSTFS_USER", tc.envUser)
			t.Setenv("RUSTFS_SECRET", "")

			resp := configureProvider(t, tc.model)

			requireNoDiagnostics(t, resp.Diagnostics)
			client, ok := resp.ResourceData.(*AllClient)
			if !ok {
				t.Fatalf("expected *AllClient in ResourceData, got %T", resp.ResourceData)
			}
			if resp.DataSourceData != resp.ResourceData {
				t.Error("expected both data source and resource data to carry the same client")
			}
			if got := aws.ToString(client.S3.Options().BaseEndpoint); got != tc.wantEndpoint {
				t.Errorf("s3 base endpoint = %q, want %q", got, tc.wantEndpoint)
			}
			// The two clients must reach the same origin whatever endpoint form
			// the user configured: compose the admin client's endpoint with the
			// scheme the S3 client uses and compare.
			adminOrigin := rustfs.RootURL(client.RustClient.Endpoint(), strings.HasPrefix(tc.wantEndpoint, "https://"))
			if adminOrigin != tc.wantEndpoint {
				t.Errorf("admin client dials %q, s3 client dials %q; both must reach the same origin", adminOrigin, tc.wantEndpoint)
			}
			creds, err := client.S3.Options().Credentials.Retrieve(context.Background())
			if err != nil {
				t.Fatalf("retrieving credentials: %v", err)
			}
			if creds.AccessKeyID != tc.wantAccessKey {
				t.Errorf("access key = %q, want %q", creds.AccessKeyID, tc.wantAccessKey)
			}
		})
	}

	t.Run("missing endpoint is reported", func(t *testing.T) {
		t.Setenv("RUSTFS_ENDPOINT", "")
		t.Setenv("RUSTFS_USER", "")

		resp := configureProvider(t, RustfsProviderModel{})

		assertErrorDiagnostic(t, resp.Diagnostics, "Missing RUSTFS endpoint",
			"Set the endpoint in the provider block or via the RUSTFS_ENDPOINT environment variable.")
		if resp.ResourceData != nil || resp.DataSourceData != nil {
			t.Error("expected no client to be handed out without an endpoint")
		}
	})

	t.Run("a scheme that contradicts ssl is rejected", func(t *testing.T) {
		t.Setenv("RUSTFS_ENDPOINT", "")

		resp := configureProvider(t, RustfsProviderModel{
			Endpoint: types.StringValue("https://cfghost:9001"),
			Ssl:      types.BoolValue(false),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid RUSTFS endpoint",
			`endpoint "https://cfghost:9001" specifies scheme "https" but ssl = false`)
		if resp.ResourceData != nil || resp.DataSourceData != nil {
			t.Error("expected no client to be handed out for a contradictory endpoint")
		}
	})

	t.Run("an endpoint with a path is rejected", func(t *testing.T) {
		t.Setenv("RUSTFS_ENDPOINT", "")

		resp := configureProvider(t, RustfsProviderModel{
			Endpoint: types.StringValue("https://cfghost:9001/admin"),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid RUSTFS endpoint",
			"only host or host:port is accepted, without a path, query or fragment")
	})
}

func TestProviderMetadata(t *testing.T) {
	resp := &provider.MetadataResponse{}
	New("1.2.3")().Metadata(context.Background(), provider.MetadataRequest{}, resp)

	if resp.TypeName != "rustfs" {
		t.Errorf("type name = %q, want rustfs", resp.TypeName)
	}
	if resp.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", resp.Version)
	}
}

// TestProviderRegistersEveryType asserts the registration contract Terraform
// sees: one entry per resource and data source, each with a unique rustfs_ name.
func TestProviderRegistersEveryType(t *testing.T) {
	p := New("test")()

	resourceNames := map[string]string{}
	for _, factory := range p.Resources(context.Background()) {
		r := factory()
		meta := &resource.MetadataResponse{}
		r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, meta)

		if !strings.HasPrefix(meta.TypeName, "rustfs_") {
			t.Errorf("resource type name %q does not carry the rustfs_ prefix", meta.TypeName)
		}
		if previous, dup := resourceNames[meta.TypeName]; dup {
			t.Errorf("resource type name %q registered twice (%s)", meta.TypeName, previous)
		}
		resourceNames[meta.TypeName] = fmt.Sprintf("%T", r)
	}
	if len(resourceNames) == 0 {
		t.Fatal("expected at least one registered resource")
	}

	dataSourceNames := map[string]struct{}{}
	for _, factory := range p.DataSources(context.Background()) {
		d := factory()
		meta := &datasource.MetadataResponse{}
		d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, meta)

		if !strings.HasPrefix(meta.TypeName, "rustfs_") {
			t.Errorf("data source type name %q does not carry the rustfs_ prefix", meta.TypeName)
		}
		if _, dup := dataSourceNames[meta.TypeName]; dup {
			t.Errorf("data source type name %q registered twice", meta.TypeName)
		}
		dataSourceNames[meta.TypeName] = struct{}{}
	}
	if len(dataSourceNames) == 0 {
		t.Fatal("expected at least one registered data source")
	}
}
