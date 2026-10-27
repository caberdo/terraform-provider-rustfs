// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

const (
	// FeatureOIDCConfig is the OIDC provider list
	// (GET /rustfs/admin/v3/oidc/config).
	FeatureOIDCConfig Feature = "OIDC provider configuration"

	// FeatureOIDCProviderConfig is the per-provider upsert and delete route
	// (PUT|DELETE /rustfs/admin/v3/oidc/config/{provider_id}).
	FeatureOIDCProviderConfig Feature = "OIDC provider mutation"

	// FeatureOIDCValidation is the discovery check
	// (POST /rustfs/admin/v3/oidc/validate).
	FeatureOIDCValidation Feature = "OIDC provider validation"
)

// OIDCProvider is one effective OIDC provider as GET /oidc/config reports it.
//
// The server never returns client_secret: ClientSecretConfigured says whether a
// secret is stored, so the provider can preserve the configured value in state
// without a permanent diff.
type OIDCProvider struct {
	ProviderID             string   `json:"provider_id"`
	Source                 string   `json:"source"`
	Editable               bool     `json:"editable"`
	Enabled                bool     `json:"enabled"`
	DisplayName            string   `json:"display_name"`
	ConfigURL              string   `json:"config_url"`
	Issuer                 *string  `json:"issuer"`
	ClientID               string   `json:"client_id"`
	ClientSecretConfigured bool     `json:"client_secret_configured"`
	Scopes                 []string `json:"scopes"`
	OtherAudiences         []string `json:"other_audiences"`
	RedirectURI            *string  `json:"redirect_uri"`
	RedirectURIDynamic     bool     `json:"redirect_uri_dynamic"`
	ClaimName              string   `json:"claim_name"`
	ClaimPrefix            string   `json:"claim_prefix"`
	RolePolicy             string   `json:"role_policy"`
	GroupsClaim            string   `json:"groups_claim"`
	RolesClaim             string   `json:"roles_claim"`
	EmailClaim             string   `json:"email_claim"`
	UsernameClaim          string   `json:"username_claim"`
	HideFromUI             bool     `json:"hide_from_ui"`
}

// OIDCConfig is the effective OIDC configuration. RestartRequired reports that
// persisted changes differ from the configuration the running server loaded.
type OIDCConfig struct {
	Providers       []OIDCProvider `json:"providers"`
	RestartRequired bool           `json:"restart_required"`
}

// OIDCProviderInput is the body PUT /oidc/config/{provider_id} accepts. It
// mirrors the server's serde struct, which denies unknown fields. Every field
// is sent explicitly so an update does not silently fall back to a server
// default. A nil ClientSecret makes the server keep the stored secret.
type OIDCProviderInput struct {
	Enabled            bool     `json:"enabled"`
	DisplayName        string   `json:"display_name"`
	ConfigURL          string   `json:"config_url"`
	Issuer             *string  `json:"issuer"`
	ClientID           string   `json:"client_id"`
	ClientSecret       *string  `json:"client_secret"`
	Scopes             []string `json:"scopes"`
	OtherAudiences     []string `json:"other_audiences"`
	RedirectURI        *string  `json:"redirect_uri"`
	RedirectURIDynamic bool     `json:"redirect_uri_dynamic"`
	ClaimName          string   `json:"claim_name"`
	ClaimPrefix        string   `json:"claim_prefix"`
	RolePolicy         string   `json:"role_policy"`
	GroupsClaim        string   `json:"groups_claim"`
	RolesClaim         string   `json:"roles_claim"`
	EmailClaim         string   `json:"email_claim"`
	UsernameClaim      string   `json:"username_claim"`
	HideFromUI         bool     `json:"hide_from_ui"`
}

// OIDCProviderValidationInput is the body POST /oidc/validate accepts.
type OIDCProviderValidationInput struct {
	ProviderID string `json:"provider_id"`
	OIDCProviderInput
}

// OIDCValidation is the result of POST /oidc/validate. The endpoint either
// answers with this document or fails the request, so Valid is always true on
// success.
type OIDCValidation struct {
	Valid                 bool    `json:"valid"`
	Message               string  `json:"message"`
	Issuer                *string `json:"issuer"`
	AuthorizationEndpoint *string `json:"authorization_endpoint"`
	TokenEndpoint         *string `json:"token_endpoint"`
}

// ListOIDCConfig reads every effective OIDC provider
// (GET /rustfs/admin/v3/oidc/config).
func (c *RustfsAdmin) ListOIDCConfig(ctx context.Context) (OIDCConfig, error) {
	reqData := RequestData{
		Method:  http.MethodGet,
		RelPath: "oidc/config",
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return OIDCConfig{}, c.featureError(ctx, err, FeatureOIDCConfig)
	}
	defer drainClose(resp)

	var config OIDCConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return OIDCConfig{}, err
	}
	return config, nil
}

// GetOIDCProvider returns one provider by id and whether it exists. The server
// has no GET-by-id route (it answers 501), so this reads the list.
func (c *RustfsAdmin) GetOIDCProvider(ctx context.Context, providerID string) (OIDCProvider, bool, error) {
	config, err := c.ListOIDCConfig(ctx)
	if err != nil {
		return OIDCProvider{}, false, err
	}
	for _, provider := range config.Providers {
		if provider.ProviderID == providerID {
			return provider, true, nil
		}
	}
	return OIDCProvider{}, false, nil
}

// UpsertOIDCProvider creates or replaces one provider
// (PUT /rustfs/admin/v3/oidc/config/{provider_id}).
func (c *RustfsAdmin) UpsertOIDCProvider(ctx context.Context, providerID string, input OIDCProviderInput) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}

	reqData := RequestData{
		Method:  http.MethodPut,
		RelPath: "oidc/config/" + url.PathEscape(providerID),
		Content: body,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return c.featureError(ctx, err, FeatureOIDCProviderConfig)
	}
	drainClose(resp)
	return nil
}

// DeleteOIDCProvider deletes one provider
// (DELETE /rustfs/admin/v3/oidc/config/{provider_id}). The server answers 400
// InvalidRequest "provider not found" when the provider is already gone.
func (c *RustfsAdmin) DeleteOIDCProvider(ctx context.Context, providerID string) error {
	reqData := RequestData{
		Method:  http.MethodDelete,
		RelPath: "oidc/config/" + url.PathEscape(providerID),
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return c.featureError(ctx, err, FeatureOIDCProviderConfig)
	}
	drainClose(resp)
	return nil
}

// ValidateOIDCProvider runs discovery for a provider configuration without
// persisting it (POST /rustfs/admin/v3/oidc/validate).
func (c *RustfsAdmin) ValidateOIDCProvider(ctx context.Context, input OIDCProviderValidationInput) (OIDCValidation, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return OIDCValidation{}, err
	}

	reqData := RequestData{
		Method:  http.MethodPost,
		RelPath: "oidc/validate",
		Content: body,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return OIDCValidation{}, c.featureError(ctx, err, FeatureOIDCValidation)
	}
	defer drainClose(resp)

	var validation OIDCValidation
	if err := json.NewDecoder(resp.Body).Decode(&validation); err != nil {
		return OIDCValidation{}, err
	}
	return validation, nil
}

func init() {
	// Probed live on RustFS 1.0.1, 1.0.2 and 1.0.3: the list, mutation and
	// validation routes all exist and work. The generic /idp-config route is
	// not modelled: its writes answer 501 for both openid and ldap, and the
	// typed OIDC routes are the only surface that persists a provider.
	registerFeature(FeatureOIDCConfig, featureSpec{
		route:      "GET /rustfs/admin/v3/oidc/config",
		minVersion: &Version{Major: 1, Minor: 0, Patch: 1, Raw: "1.0.1"},
	})
	registerFeature(FeatureOIDCProviderConfig, featureSpec{
		route:      "PUT|DELETE /rustfs/admin/v3/oidc/config/{provider_id}",
		minVersion: &Version{Major: 1, Minor: 0, Patch: 1, Raw: "1.0.1"},
	})
	registerFeature(FeatureOIDCValidation, featureSpec{
		route:      "POST /rustfs/admin/v3/oidc/validate",
		minVersion: &Version{Major: 1, Minor: 0, Patch: 1, Raw: "1.0.1"},
	})
}
