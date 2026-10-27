// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

// The client never verifies these values (httptest does not check SigV4), so
// they only label the requests. They are read from the environment when set and
// otherwise generated, never embedded as literal credentials.
var (
	oidcTestAccessKey    = oidcTestCredential("RUSTFS_TEST_OIDC_ACCESS_KEY", 'u')
	oidcTestSecretKey    = oidcTestCredential("RUSTFS_TEST_OIDC_SECRET_KEY", 'v')
	oidcTestClientSecret = oidcTestCredential("RUSTFS_TEST_OIDC_CLIENT_SECRET", 's')
)

func oidcTestCredential(env string, fill byte) string {
	if value := os.Getenv(env); value != "" {
		return value
	}
	return strings.Repeat(string(fill), 20)
}

// oidcProviderPayload is the body RustFS 1.0.3 returns on
// GET /rustfs/admin/v3/oidc/config, copied from a live server.
const oidcProviderPayload = `{"providers":[{"provider_id":"keycloak","source":"persisted",` +
	`"editable":true,"enabled":true,"display_name":"Keycloak",` +
	`"config_url":"https://kc.example.com/realms/rustfs/.well-known/openid-configuration",` +
	`"issuer":null,"client_id":"rustfs","client_secret_configured":true,` +
	`"scopes":["openid","profile","email"],"other_audiences":["aud1"],` +
	`"redirect_uri":"https://rustfs.example.com/rustfs/admin/v3/oidc/callback/keycloak",` +
	`"redirect_uri_dynamic":false,"claim_name":"groups","claim_prefix":"pfx",` +
	`"role_policy":"readwrite","groups_claim":"groups","roles_claim":"roles",` +
	`"email_claim":"email","username_claim":"preferred_username","hide_from_ui":true}],` +
	`"restart_required":true}`

const oidcAccessDeniedBody = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>nope</Message></Error>`

func newOIDCClient(t *testing.T, handler http.HandlerFunc) rustfs.RustfsAdmin {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    oidcTestAccessKey,
		AccessSecret: oidcTestSecretKey,
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
}

func TestListOIDCConfig(t *testing.T) {
	var method, requestPath string
	client := newOIDCClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, requestPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, oidcProviderPayload)
	})

	config, err := client.ListOIDCConfig(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if method != http.MethodGet || requestPath != "/rustfs/admin/v3/oidc/config" {
		t.Errorf("request = %s %s", method, requestPath)
	}
	if len(config.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(config.Providers))
	}
	provider := config.Providers[0]
	if provider.ProviderID != "keycloak" || provider.Source != "persisted" || !provider.Editable {
		t.Errorf("unexpected provider identity: %+v", provider)
	}
	if !provider.ClientSecretConfigured {
		t.Error("client_secret_configured is false")
	}
	if provider.Issuer != nil {
		t.Errorf("issuer = %v, want null", provider.Issuer)
	}
	if provider.RedirectURI == nil || *provider.RedirectURI != "https://rustfs.example.com/rustfs/admin/v3/oidc/callback/keycloak" {
		t.Errorf("redirect_uri = %v", provider.RedirectURI)
	}
	if !config.RestartRequired {
		t.Error("restart_required is false")
	}
}

func TestGetOIDCProvider(t *testing.T) {
	client := newOIDCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, oidcProviderPayload)
	})

	provider, found, err := client.GetOIDCProvider(context.Background(), "keycloak")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !found || provider.ClientID != "rustfs" {
		t.Errorf("found = %v, provider = %+v", found, provider)
	}

	_, found, err = client.GetOIDCProvider(context.Background(), "missing")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if found {
		t.Error("missing provider reported as found")
	}
}

func TestUpsertOIDCProvider(t *testing.T) {
	var method, requestPath string
	var body map[string]any
	client := newOIDCClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, requestPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"message":"OIDC provider saved","restart_required":true}`)
	})

	secret := oidcTestClientSecret
	input := rustfs.OIDCProviderInput{
		Enabled:            true,
		DisplayName:        "Keycloak",
		ConfigURL:          "https://kc.example.com/realms/rustfs/.well-known/openid-configuration",
		ClientID:           "rustfs",
		ClientSecret:       &secret,
		Scopes:             []string{"openid", "profile", "email"},
		OtherAudiences:     []string{},
		RedirectURIDynamic: false,
		RedirectURI:        new("https://rustfs.example.com/callback"),
		ClaimName:          "groups",
		GroupsClaim:        "groups",
		EmailClaim:         "email",
		UsernameClaim:      "preferred_username",
	}

	if err := client.UpsertOIDCProvider(context.Background(), "keycloak", input); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if method != http.MethodPut || requestPath != "/rustfs/admin/v3/oidc/config/keycloak" {
		t.Errorf("request = %s %s", method, requestPath)
	}
	if body["client_secret"] != oidcTestClientSecret {
		t.Error("client_secret in the request body does not match the configured fixture")
	}
	if body["config_url"] != input.ConfigURL || body["client_id"] != "rustfs" {
		t.Errorf("unexpected body: %v", body)
	}
	// The server's serde struct denies unknown fields, so every key must be one
	// of the documented fields.
	allowed := map[string]bool{
		"enabled": true, "display_name": true, "config_url": true, "issuer": true,
		"client_id": true, "client_secret": true, "scopes": true, "other_audiences": true,
		"redirect_uri": true, "redirect_uri_dynamic": true, "claim_name": true,
		"claim_prefix": true, "role_policy": true, "groups_claim": true, "roles_claim": true,
		"email_claim": true, "username_claim": true, "hide_from_ui": true,
	}
	for key := range body {
		if !allowed[key] {
			t.Errorf("unexpected body field %q", key)
		}
	}
}

func TestDeleteOIDCProviderMissingStaysAnAPIError(t *testing.T) {
	client := newOIDCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>InvalidRequest</Code><Message>provider not found</Message></Error>`)
	})

	err := client.DeleteOIDCProvider(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected an error")
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("a 400 must not be reported as a capability gap: %v", err)
	}
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest || apiErr.Message != "provider not found" {
		t.Fatalf("expected the APIError for the missing provider, got %v", err)
	}
}

func TestValidateOIDCProvider(t *testing.T) {
	var method, requestPath string
	var body map[string]any
	client := newOIDCClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, requestPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"valid":true,"message":"OIDC configuration is valid",`+
			`"issuer":"https://accounts.google.com",`+
			`"authorization_endpoint":"https://accounts.google.com/o/oauth2/v2/auth",`+
			`"token_endpoint":"https://oauth2.googleapis.com/token"}`)
	})

	result, err := client.ValidateOIDCProvider(context.Background(), rustfs.OIDCProviderValidationInput{
		ProviderID: "probe",
		OIDCProviderInput: rustfs.OIDCProviderInput{
			ConfigURL: "https://accounts.google.com/.well-known/openid-configuration",
			ClientID:  "test",
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	if method != http.MethodPost || requestPath != "/rustfs/admin/v3/oidc/validate" {
		t.Errorf("request = %s %s", method, requestPath)
	}
	if body["provider_id"] != "probe" {
		t.Errorf("provider_id = %v", body["provider_id"])
	}
	if !result.Valid || result.Issuer == nil || *result.Issuer != "https://accounts.google.com" {
		t.Errorf("unexpected validation result: %+v", result)
	}
}

func TestListOIDCConfigInvalidJSON(t *testing.T) {
	client := newOIDCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{`)
	})

	if _, err := client.ListOIDCConfig(context.Background()); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestListOIDCConfigNotImplementedBecomesUnsupportedFeature(t *testing.T) {
	client := newOIDCClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/healthinfo") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.0"}`)
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = io.WriteString(w, notImplementedBody)
	})

	_, err := client.ListOIDCConfig(context.Background())
	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureOIDCConfig {
		t.Errorf("feature = %q", unsupported.Feature)
	}
}

func TestGetOIDCProviderListError(t *testing.T) {
	client := newOIDCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom","type":"InternalError","code":500}}`)
	})

	if _, _, err := client.GetOIDCProvider(context.Background(), "keycloak"); err == nil {
		t.Fatal("expected the list error to propagate")
	}
}

func TestUpsertOIDCProviderForbiddenStaysAnAPIError(t *testing.T) {
	client := newOIDCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, oidcAccessDeniedBody)
	})

	err := client.UpsertOIDCProvider(context.Background(), "keycloak", rustfs.OIDCProviderInput{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("403 must not be reported as a capability gap: %v", err)
	}
}

func TestDeleteOIDCProviderSuccessAndForbidden(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		client := newOIDCClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Errorf("method = %s, want DELETE", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"success":true,"message":"OIDC provider deleted","restart_required":true}`)
		})

		if err := client.DeleteOIDCProvider(context.Background(), "keycloak"); err != nil {
			t.Fatalf("delete: %v", err)
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		client := newOIDCClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, oidcAccessDeniedBody)
		})

		err := client.DeleteOIDCProvider(context.Background(), "keycloak")
		if err == nil {
			t.Fatal("expected an error")
		}
		if rustfs.IsUnsupportedFeature(err) {
			t.Fatalf("403 must not be reported as a capability gap: %v", err)
		}
	})
}

func TestValidateOIDCProviderInvalidJSON(t *testing.T) {
	client := newOIDCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{`)
	})

	if _, err := client.ValidateOIDCProvider(context.Background(), rustfs.OIDCProviderValidationInput{}); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestValidateOIDCProviderForbiddenStaysAnAPIError(t *testing.T) {
	client := newOIDCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, oidcAccessDeniedBody)
	})

	_, err := client.ValidateOIDCProvider(context.Background(), rustfs.OIDCProviderValidationInput{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if rustfs.IsUnsupportedFeature(err) {
		t.Fatalf("403 must not be reported as a capability gap: %v", err)
	}
}

// TestOIDCNotImplementedBecomesUnsupportedFeature proves the mutation route is
// gated like every other capability: a 501 names the feature, the route and the
// server version instead of leaking the raw XML payload.
func TestOIDCNotImplementedBecomesUnsupportedFeature(t *testing.T) {
	client := newOIDCClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/healthinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.0"}`)
		default:
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented.</Message></Error>`)
		}
	})

	err := client.UpsertOIDCProvider(context.Background(), "probe", rustfs.OIDCProviderInput{})
	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureOIDCProviderConfig {
		t.Errorf("feature = %q", unsupported.Feature)
	}
	if want := "PUT|DELETE /rustfs/admin/v3/oidc/config/{provider_id}"; unsupported.Route != want {
		t.Errorf("route = %q, want %q", unsupported.Route, want)
	}
	if unsupported.MinVersion == nil || unsupported.MinVersion.String() != "1.0.1" {
		t.Errorf("min version = %v, want 1.0.1", unsupported.MinVersion)
	}
	if unsupported.ServerVersion != "1.0.0" {
		t.Errorf("server version = %q, want 1.0.0", unsupported.ServerVersion)
	}
}
