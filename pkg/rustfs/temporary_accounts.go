// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// User provider values the STS admin routes accept. RustFS classifies a
// temporary credential by the claims it carries: `ldap:*` claims make it
// `ldap`, an OpenID `sub` claim makes it `openid`, and everything else is
// `builtin`. `openid` covers OIDC providers.
const (
	UserProviderBuiltin = "builtin"
	UserProviderLDAP    = "ldap"
	UserProviderOpenID  = "openid"
)

// Kinds of access key the bulk listing can return.
const (
	AccessKeyKindSTS            = "sts"
	AccessKeyKindServiceAccount = "service-account"
)

// Feature names for the STS surface.
const (
	// FeatureAccessKeyBulk is the generic bulk access-key listing behind
	// rustfs_temporary_accounts (GET /rustfs/admin/v3/list-access-keys-bulk).
	FeatureAccessKeyBulk Feature = "the bulk access-key listing"

	// FeatureLDAPAccessKeyBulk is the provider-scoped LDAP listing
	// (GET /rustfs/admin/v3/idp/ldap/list-access-keys-bulk). It is a separate
	// feature so a server that registers the generic route but not this one
	// names the route that is actually missing.
	FeatureLDAPAccessKeyBulk Feature = "the LDAP bulk access-key listing"

	// FeatureOpenIDAccessKeyBulk is the provider-scoped OpenID listing
	// (GET /rustfs/admin/v3/idp/openid/list-access-keys-bulk).
	FeatureOpenIDAccessKeyBulk Feature = "the OpenID bulk access-key listing"

	// FeatureTemporaryAccountInfo is the per-key temporary account lookup used
	// to read an STS credential's effective policy
	// (GET /rustfs/admin/v3/temporary-account-info).
	FeatureTemporaryAccountInfo Feature = "temporary account information"

	// FeatureTokenRevocation deletes STS credentials for a parent user under
	// one provider (POST /rustfs/admin/v3/revoke-tokens/{user_provider}).
	FeatureTokenRevocation Feature = "STS token revocation"
)

func init() {
	// The routes ship from RustFS 1.0.0 on, which is older than the oldest
	// release the provider is tested against (1.0.1). A server below that floor
	// is refused with a version-aware diagnostic instead of an opaque 404.
	minVersion := &Version{Major: 1, Minor: 0, Patch: 1}

	registerFeature(FeatureAccessKeyBulk, featureSpec{
		route:      "GET /rustfs/admin/v3/list-access-keys-bulk",
		minVersion: minVersion,
	})
	registerFeature(FeatureLDAPAccessKeyBulk, featureSpec{
		route:      "GET /rustfs/admin/v3/idp/ldap/list-access-keys-bulk",
		minVersion: minVersion,
	})
	registerFeature(FeatureOpenIDAccessKeyBulk, featureSpec{
		route:      "GET /rustfs/admin/v3/idp/openid/list-access-keys-bulk",
		minVersion: minVersion,
	})
	registerFeature(FeatureTemporaryAccountInfo, featureSpec{
		route:      "GET /rustfs/admin/v3/temporary-account-info",
		minVersion: minVersion,
	})
	registerFeature(FeatureTokenRevocation, featureSpec{
		route:      "POST /rustfs/admin/v3/revoke-tokens/{user_provider}",
		minVersion: minVersion,
	})
}

// TemporaryAccountListOptions narrows a bulk access-key listing.
type TemporaryAccountListOptions struct {
	// UserProvider restricts the listing to one provider: builtin, ldap or
	// openid. Empty lists every provider.
	UserProvider string
	// ParentUser restricts the listing to access keys owned by this user.
	// Empty lists every parent user the server reports.
	ParentUser string
	// IncludeServiceAccounts adds non-temporary service accounts to the
	// listing. Without it only STS credentials are returned.
	IncludeServiceAccounts bool
}

// TemporaryAccount is one access key reported by the bulk listing. It carries
// identity metadata only: no secret key or session token is ever returned by
// the admin API for a listed key.
type TemporaryAccount struct {
	AccessKey     string
	ParentUser    string
	UserProvider  string
	Kind          string
	AccountStatus string
	ImpliedPolicy bool
	Name          string
	Description   string
	Expiration    string
	// Policy is the effective session policy of the key. It is empty unless
	// GetTemporaryAccountInfo was called for the key.
	Policy string
}

// TokenRevocation is the server's answer to a revoke-tokens request.
type TokenRevocation struct {
	UserProvider string
	User         string
	Revoked      int64
}

// bulkAccessKey is the wire shape of one entry in a list-access-keys-bulk
// answer. The optional fields are pointers because RustFS serializes an absent
// name, description or expiration as JSON null.
type bulkAccessKey struct {
	ParentUser    string  `json:"parentUser"`
	AccountStatus string  `json:"accountStatus"`
	ImpliedPolicy bool    `json:"impliedPolicy"`
	AccessKey     string  `json:"accessKey"`
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Expiration    *string `json:"expiration"`
}

// bulkAccessKeys is the per-parent-user entry of a list-access-keys-bulk
// answer: an object keyed by parent user.
type bulkAccessKeys struct {
	ServiceAccounts []bulkAccessKey `json:"serviceAccounts"`
	STSKeys         []bulkAccessKey `json:"stsKeys"`
}

// temporaryAccountInfoPayload is the wire shape of
// GET /rustfs/admin/v3/temporary-account-info. It deliberately has no access
// key or secret material: the route describes the credential named by the
// request.
type temporaryAccountInfoPayload struct {
	ParentUser    string  `json:"parentUser"`
	AccountStatus string  `json:"accountStatus"`
	ImpliedPolicy bool    `json:"impliedPolicy"`
	Policy        *string `json:"policy"`
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Expiration    *string `json:"expiration"`
}

// ListTemporaryAccounts lists temporary (STS) access keys, and optionally the
// service accounts stored beside them, through the bulk access-key routes.
//
// RustFS does not expose a route that lists every temporary account:
// /temporary-account-info takes one access key at a time. The listing route is
// /list-access-keys-bulk with listType=sts-only.
//
// Provider attribution comes from the server's own classification: the
// provider-scoped /idp/ldap and /idp/openid bulk routes only return credentials
// whose claims match that provider, so anything the generic route reports and
// neither scoped route does is builtin. All three routes enumerate the same
// IAM users, so the subtraction is exact wherever the server can see the key.
func (c *RustfsAdmin) ListTemporaryAccounts(ctx context.Context, options TemporaryAccountListOptions) ([]TemporaryAccount, error) {
	switch options.UserProvider {
	case "", UserProviderBuiltin, UserProviderLDAP, UserProviderOpenID:
	default:
		return nil, fmt.Errorf(
			"unsupported user provider %q: expected %q, %q or %q",
			options.UserProvider, UserProviderBuiltin, UserProviderLDAP, UserProviderOpenID,
		)
	}

	listType := "sts-only"
	if options.IncludeServiceAccounts {
		listType = "all"
	}

	accounts := make(map[string]TemporaryAccount)
	scoped := make(map[string]bool)

	// The scoped routes also run when only builtin was asked for: their answers
	// are what makes the generic route's builtin attribution exact.
	needLDAP := options.UserProvider == "" || options.UserProvider == UserProviderLDAP ||
		options.UserProvider == UserProviderBuiltin
	needOpenID := options.UserProvider == "" || options.UserProvider == UserProviderOpenID ||
		options.UserProvider == UserProviderBuiltin
	needBuiltin := options.UserProvider == "" || options.UserProvider == UserProviderBuiltin

	type scopedFetch struct {
		relPath  string
		feature  Feature
		provider string
	}
	fetches := make([]scopedFetch, 0, 2)
	if needLDAP {
		fetches = append(fetches, scopedFetch{
			relPath:  "idp/ldap/list-access-keys-bulk",
			feature:  FeatureLDAPAccessKeyBulk,
			provider: UserProviderLDAP,
		})
	}
	if needOpenID {
		fetches = append(fetches, scopedFetch{
			relPath:  "idp/openid/list-access-keys-bulk",
			feature:  FeatureOpenIDAccessKeyBulk,
			provider: UserProviderOpenID,
		})
	}

	// The scoped routes are independent, so read them concurrently. The merge
	// below stays sequential, which keeps attribution and error precedence
	// deterministic.
	results := make([]struct {
		entries []TemporaryAccount
		err     error
	}, len(fetches))
	if len(fetches) > 1 {
		var wg sync.WaitGroup
		for i := range fetches {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i].entries, results[i].err = c.listAccessKeysBulk(
					ctx, fetches[i].relPath, fetches[i].feature, listType, options.ParentUser,
				)
			}(i)
		}
		wg.Wait()
	} else {
		for i := range fetches {
			results[i].entries, results[i].err = c.listAccessKeysBulk(
				ctx, fetches[i].relPath, fetches[i].feature, listType, options.ParentUser,
			)
		}
	}

	for i, fetch := range fetches {
		if results[i].err != nil {
			return nil, results[i].err
		}
		for _, entry := range results[i].entries {
			entry.UserProvider = fetch.provider
			scoped[entry.AccessKey] = true
			if options.UserProvider != UserProviderBuiltin {
				accounts[entry.AccessKey] = entry
			}
		}
	}
	if needBuiltin {
		entries, err := c.listAccessKeysBulk(ctx, "list-access-keys-bulk", FeatureAccessKeyBulk, listType, options.ParentUser)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if scoped[entry.AccessKey] {
				continue
			}
			entry.UserProvider = UserProviderBuiltin
			accounts[entry.AccessKey] = entry
		}
	}

	listed := make([]TemporaryAccount, 0, len(accounts))
	for _, account := range accounts {
		listed = append(listed, account)
	}
	sort.Slice(listed, func(i, j int) bool {
		if listed[i].ParentUser != listed[j].ParentUser {
			return listed[i].ParentUser < listed[j].ParentUser
		}
		return listed[i].AccessKey < listed[j].AccessKey
	})
	return listed, nil
}

// listAccessKeysBulk reads one bulk access-key route. An empty parentUser asks
// for every user (all=true); otherwise the request names that user directly, so
// an externally provisioned identity the server does not persist as an IAM user
// is still listed.
func (c *RustfsAdmin) listAccessKeysBulk(ctx context.Context, relPath string, feature Feature, listType, parentUser string) ([]TemporaryAccount, error) {
	parentUser = strings.TrimSpace(parentUser)

	query := url.Values{}
	query.Set("listType", listType)
	if parentUser == "" {
		query.Set("all", "true")
	} else {
		query.Add("users", parentUser)
	}

	reqData := RequestData{
		Method:      http.MethodGet,
		RelPath:     relPath,
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureError(ctx, err, feature)
	}
	defer drainClose(resp)

	var payload map[string]bulkAccessKeys
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode bulk access-key listing: %w", err)
	}

	accounts := make([]TemporaryAccount, 0, len(payload))
	for parentUser, entry := range payload {
		for _, key := range entry.STSKeys {
			accounts = append(accounts, temporaryAccountFromBulk(parentUser, key, AccessKeyKindSTS))
		}
		for _, key := range entry.ServiceAccounts {
			accounts = append(accounts, temporaryAccountFromBulk(parentUser, key, AccessKeyKindServiceAccount))
		}
	}
	return accounts, nil
}

func temporaryAccountFromBulk(parentUser string, key bulkAccessKey, kind string) TemporaryAccount {
	owner := key.ParentUser
	if owner == "" {
		owner = parentUser
	}
	return TemporaryAccount{
		AccessKey:     key.AccessKey,
		ParentUser:    owner,
		Kind:          kind,
		AccountStatus: key.AccountStatus,
		ImpliedPolicy: key.ImpliedPolicy,
		Name:          stringValue(key.Name),
		Description:   stringValue(key.Description),
		Expiration:    stringValue(key.Expiration),
	}
}

// GetTemporaryAccountInfo reads one temporary account's effective policy.
//
// RustFS answers this route for an STS credential; a service account has its
// own info-service-account route. The answer never carries the secret key or
// session token of the credential.
func (c *RustfsAdmin) GetTemporaryAccountInfo(ctx context.Context, accessKey string) (TemporaryAccount, error) {
	if accessKey == "" {
		return TemporaryAccount{}, errors.New("access key is required")
	}

	query := url.Values{}
	query.Set("accessKey", accessKey)
	reqData := RequestData{
		Method:      http.MethodGet,
		RelPath:     "temporary-account-info",
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return TemporaryAccount{}, c.featureError(ctx, err, FeatureTemporaryAccountInfo)
	}
	defer drainClose(resp)

	var payload temporaryAccountInfoPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return TemporaryAccount{}, fmt.Errorf("decode temporary account information: %w", err)
	}
	return TemporaryAccount{
		AccessKey:     accessKey,
		ParentUser:    payload.ParentUser,
		AccountStatus: payload.AccountStatus,
		ImpliedPolicy: payload.ImpliedPolicy,
		Name:          stringValue(payload.Name),
		Description:   stringValue(payload.Description),
		Expiration:    stringValue(payload.Expiration),
		Policy:        stringValue(payload.Policy),
	}, nil
}

// RevokeTokens deletes every STS credential a parent user holds under one
// provider. Revocation is immediate: the next request signed with a revoked
// credential fails authentication.
//
// The provider has to be named explicitly. RustFS accepts builtin, ldap and
// openid; a provider it does not support is refused before any request is sent.
// The user has to be named too: the server would otherwise default to the
// caller's own identity, which turns a mistake into an operator lockout.
func (c *RustfsAdmin) RevokeTokens(ctx context.Context, userProvider, user string) (TokenRevocation, error) {
	switch userProvider {
	case UserProviderBuiltin, UserProviderLDAP, UserProviderOpenID:
	default:
		return TokenRevocation{}, fmt.Errorf(
			"unsupported user provider %q: expected %q, %q or %q",
			userProvider, UserProviderBuiltin, UserProviderLDAP, UserProviderOpenID,
		)
	}
	if strings.TrimSpace(user) == "" {
		return TokenRevocation{}, errors.New("user is required: refusing to revoke the caller's own tokens")
	}

	query := url.Values{}
	query.Set("user", strings.TrimSpace(user))
	query.Set("fullRevoke", "true")
	reqData := RequestData{
		Method:      http.MethodPost,
		RelPath:     "revoke-tokens/" + url.PathEscape(userProvider),
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return TokenRevocation{}, c.featureError(ctx, err, FeatureTokenRevocation)
	}
	defer drainClose(resp)

	var payload struct {
		Revoked      int64  `json:"revoked"`
		UserProvider string `json:"userProvider"`
		User         string `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return TokenRevocation{}, fmt.Errorf("decode token revocation: %w", err)
	}
	return TokenRevocation{
		UserProvider: payload.UserProvider,
		User:         payload.User,
		Revoked:      payload.Revoked,
	}, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
