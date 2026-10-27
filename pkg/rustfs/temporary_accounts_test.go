// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The payloads below follow the RustFS 1.0.x wire format:
// list-access-keys-bulk answers with an object keyed by parent user, and each
// entry carries stsKeys/serviceAccounts arrays. Absent optional fields are
// serialized as null.
const (
	ldapBulkPayload = `{"alice":{"serviceAccounts":[],"stsKeys":[` +
		`{"parentUser":"alice","accountStatus":"enabled","impliedPolicy":false,` +
		`"accessKey":"ldap-key-1","name":null,"description":null,` +
		`"expiration":"2026-10-21T12:00:00Z"}]}}`

	openIDBulkPayload = `{"bob":{"serviceAccounts":[],"stsKeys":[` +
		`{"parentUser":"bob","accountStatus":"enabled","impliedPolicy":true,` +
		`"accessKey":"openid-key-1","name":"bob-session","description":null,` +
		`"expiration":"2026-10-21T13:00:00Z"}]}}`

	genericBulkPayload = `{` +
		`"alice":{"serviceAccounts":[],"stsKeys":[` +
		`{"parentUser":"alice","accountStatus":"enabled","impliedPolicy":false,` +
		`"accessKey":"ldap-key-1","name":null,"description":null,` +
		`"expiration":"2026-10-21T12:00:00Z"}]},` +
		`"bob":{"serviceAccounts":[],"stsKeys":[` +
		`{"parentUser":"bob","accountStatus":"enabled","impliedPolicy":true,` +
		`"accessKey":"openid-key-1","name":"bob-session","description":null,` +
		`"expiration":"2026-10-21T13:00:00Z"}]},` +
		`"carol":{"serviceAccounts":[],"stsKeys":[` +
		`{"parentUser":"carol","accountStatus":"enabled","impliedPolicy":false,` +
		`"accessKey":"builtin-key-1","name":"carol-session",` +
		`"description":"CI runner","expiration":"2026-10-21T14:00:00Z"}]}}`

	temporaryAccountInfoResponse = `{"parentUser":"carol","accountStatus":"enabled",` +
		`"impliedPolicy":false,"policy":"{\"Version\":\"2012-10-17\"}",` +
		`"name":"carol-session","description":"CI runner","expiration":"2026-10-21T14:00:00Z"}`

	revokeTokensPayload = `{"revoked":2,"userProvider":"openid","user":"bob"}`
)

type observedAdminCall struct {
	method string
	path   string
	query  string
}

// temporaryAccountsServer routes by path substring and records every request.
// The map values are the response bodies; healthinfo carries a version so a
// capability-gap test gets a version-aware message.
type temporaryAccountsServer struct {
	t      *testing.T
	bodies map[string]string
	mu     sync.Mutex
	calls  []observedAdminCall
}

func (s *temporaryAccountsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, observedAdminCall{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery})
	s.mu.Unlock()

	if strings.Contains(r.URL.Path, "healthinfo") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.3"}`)
		return
	}

	// Longest fragment first: every provider-scoped route also contains the
	// generic "list-access-keys-bulk" fragment.
	fragments := make([]string, 0, len(s.bodies))
	for fragment := range s.bodies {
		fragments = append(fragments, fragment)
	}
	sort.Slice(fragments, func(i, j int) bool { return len(fragments[i]) > len(fragments[j]) })

	for _, fragment := range fragments {
		if strings.Contains(r.URL.Path, fragment) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, s.bodies[fragment])
			return
		}
	}

	s.t.Errorf("unrouted request: %s %s", r.Method, r.URL.Path)
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, `{"code":"UnroutedRequest","message":"no route"}`)
}

func newTemporaryAccountsTestClient(t *testing.T, bodies map[string]string) (RustfsAdmin, *temporaryAccountsServer) {
	t.Helper()

	handler := &temporaryAccountsServer{t: t, bodies: bodies}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := New(&RustfsAdminConfig{
		Endpoint:     server.Listener.Addr().String(),
		AccessKey:    temporaryAccountsTestCredential("RUSTFS_ACCESS_KEY", 'a'),
		AccessSecret: temporaryAccountsTestCredential("RUSTFS_SECRET", 's'),
	})
	return client, handler
}

func temporaryAccountsTestCredential(env string, fallback rune) string {
	if value := os.Getenv(env); value != "" {
		return value
	}
	return strings.Repeat(string(fallback), 16)
}

func (s *temporaryAccountsServer) takeCalls() []observedAdminCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	calls := s.calls
	s.calls = nil
	return calls
}

func TestListTemporaryAccountsAttributesEveryProvider(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, map[string]string{
		"idp/ldap/list-access-keys-bulk":   ldapBulkPayload,
		"idp/openid/list-access-keys-bulk": openIDBulkPayload,
		"list-access-keys-bulk":            genericBulkPayload,
	})

	accounts, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := server.takeCalls()
	if len(calls) != 3 {
		t.Fatalf("expected three bulk calls, got %d: %+v", len(calls), calls)
	}
	// The two scoped routes are read concurrently, so only the generic call
	// has a fixed position: it has to run after them for the subtraction.
	scopedPaths := map[string]bool{}
	for _, call := range calls[:2] {
		scopedPaths[call.path] = true
		if call.method != http.MethodGet {
			t.Errorf("scoped call method = %q", call.method)
		}
		if !strings.Contains(call.query, "all=true") || !strings.Contains(call.query, "listType=sts-only") {
			t.Errorf("scoped call query = %q", call.query)
		}
	}
	for _, fragment := range []string{"idp/ldap/list-access-keys-bulk", "idp/openid/list-access-keys-bulk"} {
		if !recorderSawPath(scopedPaths, fragment) {
			t.Errorf("missing scoped call ending in %q: %+v", fragment, calls)
		}
	}
	if !strings.HasSuffix(calls[2].path, "list-access-keys-bulk") || strings.Contains(calls[2].path, "/idp/") {
		t.Errorf("last call path = %q, want the generic route", calls[2].path)
	}
	if calls[2].method != http.MethodGet {
		t.Errorf("generic call method = %q", calls[2].method)
	}
	if !strings.Contains(calls[2].query, "all=true") || !strings.Contains(calls[2].query, "listType=sts-only") {
		t.Errorf("generic call query = %q", calls[2].query)
	}

	if len(accounts) != 3 {
		t.Fatalf("expected three accounts, got %d: %+v", len(accounts), accounts)
	}
	// Sorted by parent user: alice, bob, carol.
	wantProviders := []string{UserProviderLDAP, UserProviderOpenID, UserProviderBuiltin}
	wantKeys := []string{"ldap-key-1", "openid-key-1", "builtin-key-1"}
	for i, account := range accounts {
		if account.UserProvider != wantProviders[i] {
			t.Errorf("account %d provider = %q, want %q", i, account.UserProvider, wantProviders[i])
		}
		if account.AccessKey != wantKeys[i] {
			t.Errorf("account %d access key = %q, want %q", i, account.AccessKey, wantKeys[i])
		}
		if account.Kind != AccessKeyKindSTS {
			t.Errorf("account %d kind = %q", i, account.Kind)
		}
	}
	if accounts[2].Name != "carol-session" || accounts[2].Description != "CI runner" {
		t.Errorf("optional fields were lost: %+v", accounts[2])
	}
	if accounts[2].Expiration != "2026-10-21T14:00:00Z" {
		t.Errorf("expiration = %q", accounts[2].Expiration)
	}
	if !accounts[1].ImpliedPolicy {
		t.Error("implied_policy was not carried over")
	}
}

func TestListTemporaryAccountsFiltersOneProvider(t *testing.T) {
	t.Run("ldap", func(t *testing.T) {
		client, server := newTemporaryAccountsTestClient(t, map[string]string{
			"idp/ldap/list-access-keys-bulk": ldapBulkPayload,
		})

		accounts, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{UserProvider: UserProviderLDAP})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls := server.takeCalls(); len(calls) != 1 || !strings.HasSuffix(calls[0].path, "idp/ldap/list-access-keys-bulk") {
			t.Fatalf("expected one LDAP call, got %+v", calls)
		}
		if len(accounts) != 1 || accounts[0].UserProvider != UserProviderLDAP {
			t.Fatalf("unexpected accounts: %+v", accounts)
		}
	})

	t.Run("openid", func(t *testing.T) {
		client, server := newTemporaryAccountsTestClient(t, map[string]string{
			"idp/openid/list-access-keys-bulk": openIDBulkPayload,
		})

		accounts, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{UserProvider: UserProviderOpenID})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls := server.takeCalls(); len(calls) != 1 || !strings.HasSuffix(calls[0].path, "idp/openid/list-access-keys-bulk") {
			t.Fatalf("expected one OpenID call, got %+v", calls)
		}
		if len(accounts) != 1 || accounts[0].UserProvider != UserProviderOpenID {
			t.Fatalf("unexpected accounts: %+v", accounts)
		}
	})

	t.Run("builtin subtracts the scoped providers", func(t *testing.T) {
		client, server := newTemporaryAccountsTestClient(t, map[string]string{
			"idp/ldap/list-access-keys-bulk":   ldapBulkPayload,
			"idp/openid/list-access-keys-bulk": openIDBulkPayload,
			"list-access-keys-bulk":            genericBulkPayload,
		})

		accounts, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{UserProvider: UserProviderBuiltin})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The scoped routes are read to attribute the generic answer, not to
		// contribute accounts of their own.
		if calls := server.takeCalls(); len(calls) != 3 {
			t.Fatalf("expected three calls, got %+v", calls)
		}
		if len(accounts) != 1 || accounts[0].AccessKey != "builtin-key-1" {
			t.Fatalf("unexpected accounts: %+v", accounts)
		}
	})
}

func TestListTemporaryAccountsIncludesServiceAccounts(t *testing.T) {
	const payload = `{"dave":{"serviceAccounts":[` +
		`{"parentUser":"dave","accountStatus":"enabled","impliedPolicy":false,` +
		`"accessKey":"svc-key-1","name":null,"description":null,"expiration":null}],` +
		`"stsKeys":[` +
		`{"parentUser":"dave","accountStatus":"enabled","impliedPolicy":false,` +
		`"accessKey":"sts-key-1","name":null,"description":null,"expiration":null}]}}`

	client, server := newTemporaryAccountsTestClient(t, map[string]string{
		"idp/ldap/list-access-keys-bulk":   `{}`,
		"idp/openid/list-access-keys-bulk": `{}`,
		"list-access-keys-bulk":            payload,
	})

	accounts, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{IncludeServiceAccounts: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := server.takeCalls()
	if len(calls) != 3 {
		t.Fatalf("expected three calls, got %+v", calls)
	}
	for _, call := range calls {
		if !strings.Contains(call.query, "listType=all") {
			t.Errorf("query = %q, want listType=all", call.query)
		}
	}

	if len(accounts) != 2 {
		t.Fatalf("expected two accounts, got %+v", accounts)
	}
	kinds := map[string]string{
		accounts[0].AccessKey: accounts[0].Kind,
		accounts[1].AccessKey: accounts[1].Kind,
	}
	if kinds["sts-key-1"] != AccessKeyKindSTS {
		t.Errorf("sts kind = %q", kinds["sts-key-1"])
	}
	if kinds["svc-key-1"] != AccessKeyKindServiceAccount {
		t.Errorf("service-account kind = %q", kinds["svc-key-1"])
	}
}

func TestListTemporaryAccountsFiltersParentUser(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, map[string]string{
		"idp/ldap/list-access-keys-bulk": ldapBulkPayload,
	})

	_, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{
		UserProvider: UserProviderLDAP,
		ParentUser:   "  alice  ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := server.takeCalls()[0]
	if strings.Contains(call.query, "all=true") {
		t.Errorf("a named parent user must not use all=true: %q", call.query)
	}
	if !strings.Contains(call.query, "users=alice") {
		t.Errorf("query = %q, want users=alice", call.query)
	}
}

func TestListTemporaryAccountsRejectsUnknownProvider(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, nil)

	_, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{UserProvider: "oidc"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `unsupported user provider "oidc"`) {
		t.Errorf("error = %q", err)
	}
	if calls := server.takeCalls(); len(calls) != 0 {
		t.Errorf("an invalid provider must not reach the server: %+v", calls)
	}
}

func TestListTemporaryAccountsEmptyAnswer(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, map[string]string{
		"idp/ldap/list-access-keys-bulk":   `{}`,
		"idp/openid/list-access-keys-bulk": `{}`,
		"list-access-keys-bulk":            `{}`,
	})

	accounts, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("expected no accounts, got %+v", accounts)
	}
}

func TestListTemporaryAccountsUnsupported(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, nil)
	// Every bulk route a builtin filter reads (the two scoped routes and the
	// generic one) answers 501.
	client = withStatusForPaths(client, "list-access-keys-bulk", http.StatusNotImplemented)

	_, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{UserProvider: UserProviderBuiltin})
	if !IsUnsupportedFeature(err) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedFeatureError, got %T", err)
	}
	if unsupported.Feature != FeatureLDAPAccessKeyBulk {
		t.Errorf("feature = %q", unsupported.Feature)
	}
	if !strings.Contains(err.Error(), "GET /rustfs/admin/v3/idp/ldap/list-access-keys-bulk") {
		t.Errorf("error %q does not name the route", err)
	}
	if !strings.Contains(err.Error(), "RustFS 1.0.3") {
		t.Errorf("error %q does not name the server version", err)
	}
}

func TestListTemporaryAccountsGenericRouteUnsupported(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, map[string]string{
		"idp/ldap/list-access-keys-bulk":   `{}`,
		"idp/openid/list-access-keys-bulk": `{}`,
	})
	client = withStatusForPaths(client, "/rustfs/admin/v3/list-access-keys-bulk", http.StatusNotImplemented)

	_, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{UserProvider: UserProviderBuiltin})
	if !IsUnsupportedFeature(err) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
}

func TestListTemporaryAccountsRejectsMalformedPayload(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, map[string]string{
		"idp/ldap/list-access-keys-bulk": `{"alice":`,
	})

	_, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{UserProvider: UserProviderLDAP})
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if !strings.Contains(err.Error(), "decode bulk access-key listing") {
		t.Errorf("error = %q", err)
	}
}

func TestGetTemporaryAccountInfo(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, map[string]string{
		"temporary-account-info": temporaryAccountInfoResponse,
	})

	account, err := client.GetTemporaryAccountInfo(context.Background(), "builtin-key-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := server.takeCalls()[0]
	if call.method != http.MethodGet || !strings.HasSuffix(call.path, "temporary-account-info") {
		t.Errorf("unexpected request %s %s", call.method, call.path)
	}
	if !strings.Contains(call.query, "accessKey=builtin-key-1") {
		t.Errorf("query = %q", call.query)
	}
	if account.AccessKey != "builtin-key-1" || account.ParentUser != "carol" {
		t.Errorf("account = %+v", account)
	}
	if account.Policy != `{"Version":"2012-10-17"}` {
		t.Errorf("policy = %q", account.Policy)
	}
	if account.Name != "carol-session" || account.Description != "CI runner" {
		t.Errorf("optional fields = %+v", account)
	}
}

func TestGetTemporaryAccountInfoNullPolicy(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, map[string]string{
		"temporary-account-info": `{"parentUser":"carol","accountStatus":"enabled","impliedPolicy":true}`,
	})

	account, err := client.GetTemporaryAccountInfo(context.Background(), "builtin-key-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if account.Policy != "" || account.Expiration != "" {
		t.Errorf("null fields must decode as empty: %+v", account)
	}
	if !account.ImpliedPolicy {
		t.Error("implied_policy was not carried over")
	}
}

func TestGetTemporaryAccountInfoRequiresAccessKey(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, nil)

	_, err := client.GetTemporaryAccountInfo(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "access key is required") {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := server.takeCalls(); len(calls) != 0 {
		t.Errorf("an empty access key must not reach the server: %+v", calls)
	}
}

func TestGetTemporaryAccountInfoUnsupported(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, nil)
	client = withStatusForPaths(client, "/rustfs/admin/v3/temporary-account-info", http.StatusNotImplemented)

	_, err := client.GetTemporaryAccountInfo(context.Background(), "builtin-key-1")
	if !IsUnsupportedFeature(err) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "GET /rustfs/admin/v3/temporary-account-info") {
		t.Errorf("error %q does not name the route", err)
	}
}

func TestGetTemporaryAccountInfoRejectsMalformedPayload(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, map[string]string{
		"temporary-account-info": `{"parentUser":`,
	})

	_, err := client.GetTemporaryAccountInfo(context.Background(), "builtin-key-1")
	if err == nil || !strings.Contains(err.Error(), "decode temporary account information") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRevokeTokens(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, map[string]string{
		"revoke-tokens": revokeTokensPayload,
	})

	result, err := client.RevokeTokens(context.Background(), UserProviderOpenID, "  bob  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := server.takeCalls()[0]
	if call.method != http.MethodPost {
		t.Errorf("method = %q", call.method)
	}
	if !strings.HasSuffix(call.path, "revoke-tokens/openid") {
		t.Errorf("path = %q", call.path)
	}
	if !strings.Contains(call.query, "user=bob") || !strings.Contains(call.query, "fullRevoke=true") {
		t.Errorf("query = %q", call.query)
	}
	if result.Revoked != 2 || result.UserProvider != UserProviderOpenID || result.User != "bob" {
		t.Errorf("result = %+v", result)
	}
}

func TestRevokeTokensRejectsUnknownProvider(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, nil)

	_, err := client.RevokeTokens(context.Background(), "oidc", "bob")
	if err == nil || !strings.Contains(err.Error(), `unsupported user provider "oidc"`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := server.takeCalls(); len(calls) != 0 {
		t.Errorf("an invalid provider must not reach the server: %+v", calls)
	}
}

func TestRevokeTokensRequiresUser(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, nil)

	_, err := client.RevokeTokens(context.Background(), UserProviderBuiltin, "   ")
	if err == nil || !strings.Contains(err.Error(), "user is required") {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := server.takeCalls(); len(calls) != 0 {
		t.Errorf("an empty user must not reach the server: %+v", calls)
	}
}

func TestRevokeTokensUnsupported(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, nil)
	client = withStatusForPaths(client, "/rustfs/admin/v3/revoke-tokens/openid", http.StatusNotImplemented)

	_, err := client.RevokeTokens(context.Background(), UserProviderOpenID, "bob")
	if !IsUnsupportedFeature(err) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "POST /rustfs/admin/v3/revoke-tokens/{user_provider}") {
		t.Errorf("error %q does not name the route", err)
	}
}

func TestRevokeTokensRejectsMalformedPayload(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, map[string]string{
		"revoke-tokens": `{"revoked":`,
	})

	_, err := client.RevokeTokens(context.Background(), UserProviderOpenID, "bob")
	if err == nil || !strings.Contains(err.Error(), "decode token revocation") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// withStatusForPaths replaces the client's transport with one that answers the
// named path fragment with status and keeps the original server for the rest.
func withStatusForPaths(client RustfsAdmin, pathFragment string, status int) RustfsAdmin {
	original := client.httpClient
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, pathFragment) {
			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": []string{"application/xml"}},
				Body: io.NopCloser(strings.NewReader(
					`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`,
				)),
				Request: req,
			}, nil
		}
		return original.Do(req)
	})}
	return client
}

func TestListTemporaryAccountsFallsBackToTheMapKey(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, map[string]string{
		"idp/ldap/list-access-keys-bulk": `{"erin":{"stsKeys":[` +
			`{"accessKey":"key-b","expiration":null},` +
			`{"accessKey":"key-a","expiration":null}]}}`,
	})

	accounts, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{UserProvider: UserProviderLDAP})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("expected two accounts, got %+v", accounts)
	}
	// Same parent user on both entries exercises the access-key tie-break.
	if accounts[0].AccessKey != "key-a" || accounts[1].AccessKey != "key-b" {
		t.Errorf("accounts are not sorted by access key: %+v", accounts)
	}
	for _, account := range accounts {
		if account.ParentUser != "erin" {
			t.Errorf("parent user = %q, want the map key", account.ParentUser)
		}
	}
}

func TestListTemporaryAccountsPropagatesAProviderError(t *testing.T) {
	client, _ := newTemporaryAccountsTestClient(t, map[string]string{
		"idp/ldap/list-access-keys-bulk": `{}`,
	})
	client = withStatusForPaths(client, "idp/openid/list-access-keys-bulk", http.StatusNotImplemented)

	_, err := client.ListTemporaryAccounts(context.Background(), TemporaryAccountListOptions{})
	if !IsUnsupportedFeature(err) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "GET /rustfs/admin/v3/idp/openid/list-access-keys-bulk") {
		t.Errorf("error %q does not name the scoped route that failed", err)
	}
}

func recorderSawPath(paths map[string]bool, suffix string) bool {
	for path := range paths {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func TestListTemporaryAccountsCancelled(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.ListTemporaryAccounts(ctx, TemporaryAccountListOptions{UserProvider: UserProviderLDAP})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
	if calls := server.takeCalls(); len(calls) != 0 {
		t.Errorf("a cancelled context must not reach the server: %+v", calls)
	}
}

func TestGetTemporaryAccountInfoCancelled(t *testing.T) {
	client, server := newTemporaryAccountsTestClient(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetTemporaryAccountInfo(ctx, "ldap-key-1")
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
	if calls := server.takeCalls(); len(calls) != 0 {
		t.Errorf("a cancelled context must not reach the server: %+v", calls)
	}
}
