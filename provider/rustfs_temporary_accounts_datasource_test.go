// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Payloads follow the RustFS 1.0.x wire format: list-access-keys-bulk answers
// with an object keyed by parent user.
const (
	temporaryAccountsLDAPPayload = `{"alice":{"serviceAccounts":[],"stsKeys":[` +
		`{"parentUser":"alice","accountStatus":"enabled","impliedPolicy":false,` +
		`"accessKey":"ldap-key-1","name":null,"description":null,` +
		`"expiration":"2026-10-21T12:00:00Z"}]}}`

	temporaryAccountsOpenIDPayload = `{"bob":{"serviceAccounts":[],"stsKeys":[` +
		`{"parentUser":"bob","accountStatus":"enabled","impliedPolicy":true,` +
		`"accessKey":"openid-key-1","name":"bob-session","description":null,` +
		`"expiration":"2026-10-21T13:00:00Z"}]}}`

	temporaryAccountsGenericPayload = `{` +
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

	temporaryAccountsPolicyPayload = `{"parentUser":"carol","accountStatus":"enabled",` +
		`"impliedPolicy":false,"policy":"{\"Version\":\"2012-10-17\"}",` +
		`"name":"carol-session","description":"CI runner","expiration":"2026-10-21T14:00:00Z"}`
)

func TestTemporaryAccountsDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewTemporaryAccountsDataSource())

	attrs := sch.GetAttributes()
	if len(attrs) != 5 {
		t.Errorf("expected 5 attributes, got %d: %v", len(attrs), attrs)
	}

	for _, name := range []string{"user_provider", "parent_user", "include_service_accounts", "include_policies"} {
		attr := attrs[name]
		if attr == nil || !attr.IsOptional() || attr.IsRequired() || attr.IsComputed() {
			t.Errorf("%s must be Optional only: %+v", name, attr)
		}
	}

	provider, ok := attrs["user_provider"].(datasourceschema.StringAttribute)
	if !ok || len(provider.Validators) == 0 {
		t.Errorf("user_provider must validate its enum: %+v", attrs["user_provider"])
	}

	accounts, ok := attrs["accounts"].(datasourceschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("accounts is not a ListNestedAttribute: %T", attrs["accounts"])
	}
	if !accounts.Computed || accounts.Optional || accounts.Required {
		t.Errorf("accounts must be Computed only: %+v", accounts)
	}

	nested := accounts.NestedObject.Attributes
	want := []string{
		"access_key", "parent_user", "user_provider", "kind", "account_status",
		"implied_policy", "name", "description", "expiration", "policy",
	}
	if len(nested) != len(want) {
		t.Errorf("expected %d nested attributes, got %d: %v", len(want), len(nested), nested)
	}
	for _, name := range want {
		attr := nested[name]
		if attr == nil || !attr.IsComputed() || attr.IsOptional() || attr.IsRequired() {
			t.Errorf("accounts.%s must be Computed only: %+v", name, attr)
		}
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
	for name, attr := range nested {
		if attr.GetDescription() == "" {
			t.Errorf("attribute accounts.%s has no description", name)
		}
	}
}

func TestTemporaryAccountsDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewTemporaryAccountsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_temporary_accounts" {
		t.Errorf("expected rustfs_temporary_accounts, got %s", resp.TypeName)
	}
}

func TestTemporaryAccountsDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewTemporaryAccountsDataSource().(*TemporaryAccountsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewTemporaryAccountsDataSource().(*TemporaryAccountsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		client, _ := routedClient(t)
		d := NewTemporaryAccountsDataSource().(*TemporaryAccountsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestTemporaryAccountsDataSourceRead(t *testing.T) {
	t.Run("lists every provider sorted by parent user", func(t *testing.T) {
		client, recorder := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "idp/ldap/list-access-keys-bulk", status: http.StatusOK, body: temporaryAccountsLDAPPayload},
			requestRoute{method: http.MethodGet, pathContains: "idp/openid/list-access-keys-bulk", status: http.StatusOK, body: temporaryAccountsOpenIDPayload},
			requestRoute{method: http.MethodGet, pathContains: "v3/list-access-keys-bulk", status: http.StatusOK, body: temporaryAccountsGenericPayload},
		)
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringNull(),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolValue(false),
			IncludePolicies:        types.BoolValue(false),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})
		requireNoDiagnostics(t, resp.Diagnostics)

		if requests := recorder.snapshot(); len(requests) != 3 {
			t.Errorf("expected three bulk requests, got %v", requests)
		}

		var got TemporaryAccountsDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		var accounts []temporaryAccountModel
		if diags := got.Accounts.ElementsAs(context.Background(), &accounts, false); diags.HasError() {
			t.Fatalf("reading the accounts: %v", diags)
		}
		if len(accounts) != 3 {
			t.Fatalf("expected three accounts, got %d: %+v", len(accounts), accounts)
		}
		if accounts[0].AccessKey.ValueString() != "ldap-key-1" || accounts[0].UserProvider.ValueString() != "ldap" {
			t.Errorf("first account = %+v", accounts[0])
		}
		if accounts[1].UserProvider.ValueString() != "openid" || accounts[1].Name.ValueString() != "bob-session" {
			t.Errorf("second account = %+v", accounts[1])
		}
		if accounts[2].AccessKey.ValueString() != "builtin-key-1" || accounts[2].UserProvider.ValueString() != "builtin" {
			t.Errorf("third account = %+v", accounts[2])
		}
		if accounts[2].Kind.ValueString() != "sts" {
			t.Errorf("kind = %q", accounts[2].Kind.ValueString())
		}
		if accounts[2].Description.ValueString() != "CI runner" {
			t.Errorf("description = %q", accounts[2].Description.ValueString())
		}
		if !accounts[0].Policy.IsNull() {
			t.Errorf("policy must stay null without include_policies: %+v", accounts[0].Policy)
		}
		if !accounts[0].ImpliedPolicy.IsNull() {
			t.Errorf("implied_policy must stay null without include_policies: %+v", accounts[0].ImpliedPolicy)
		}
	})

	t.Run("accepts an empty answer", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "list-access-keys-bulk", status: http.StatusOK, body: `{}`},
		)
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringValue("ldap"),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolNull(),
			IncludePolicies:        types.BoolNull(),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})
		requireNoDiagnostics(t, resp.Diagnostics)

		var got TemporaryAccountsDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if elements := got.Accounts.Elements(); len(elements) != 0 {
			t.Errorf("expected no accounts, got %v", elements)
		}
	})

	t.Run("enriches temporary accounts with their policy", func(t *testing.T) {
		client, recorder := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "idp/ldap/list-access-keys-bulk", status: http.StatusOK, body: temporaryAccountsLDAPPayload},
			requestRoute{method: http.MethodGet, pathContains: "temporary-account-info", status: http.StatusOK, body: temporaryAccountsPolicyPayload},
		)
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringValue("ldap"),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolNull(),
			IncludePolicies:        types.BoolValue(true),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})
		requireNoDiagnostics(t, resp.Diagnostics)
		if want := "GET /rustfs/admin/v3/temporary-account-info"; !recorderSaw(recorder, want) {
			t.Errorf("missing request %q in %v", want, recorder.requests)
		}

		var got TemporaryAccountsDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		var accounts []temporaryAccountModel
		if diags := got.Accounts.ElementsAs(context.Background(), &accounts, false); diags.HasError() {
			t.Fatalf("reading the accounts: %v", diags)
		}
		if len(accounts) != 1 {
			t.Fatalf("expected one account, got %+v", accounts)
		}
		if accounts[0].Policy.ValueString() != `{"Version":"2012-10-17"}` {
			t.Errorf("policy = %q", accounts[0].Policy.ValueString())
		}
		if accounts[0].ImpliedPolicy.IsNull() {
			t.Error("implied_policy must be populated with include_policies")
		}
		if accounts[0].AccountStatus.ValueString() != "enabled" {
			t.Errorf("account_status = %q", accounts[0].AccountStatus.ValueString())
		}
	})

	t.Run("reads a service account policy from its own route", func(t *testing.T) {
		const serviceAccountPayload = `{"dave":{"serviceAccounts":[` +
			`{"parentUser":"dave","accountStatus":"enabled","impliedPolicy":false,` +
			`"accessKey":"svc-key-1","name":null,"description":null,"expiration":null}],` +
			`"stsKeys":[]}}`
		client, recorder := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "idp/ldap/list-access-keys-bulk", status: http.StatusOK, body: serviceAccountPayload},
			requestRoute{method: http.MethodGet, pathContains: "info-service-account", status: http.StatusOK, body: temporaryAccountsPolicyPayload},
		)
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringValue("ldap"),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolValue(true),
			IncludePolicies:        types.BoolValue(true),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})
		requireNoDiagnostics(t, resp.Diagnostics)
		if want := "GET /rustfs/admin/v3/info-service-account"; !recorderSaw(recorder, want) {
			t.Errorf("missing request %q in %v", want, recorder.requests)
		}

		var got TemporaryAccountsDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		var accounts []temporaryAccountModel
		if diags := got.Accounts.ElementsAs(context.Background(), &accounts, false); diags.HasError() {
			t.Fatalf("reading the accounts: %v", diags)
		}
		if len(accounts) != 1 || accounts[0].Kind.ValueString() != "service-account" {
			t.Fatalf("unexpected accounts: %+v", accounts)
		}
		if accounts[0].Policy.ValueString() != `{"Version":"2012-10-17"}` {
			t.Errorf("policy = %q", accounts[0].Policy.ValueString())
		}
	})

	t.Run("reports a capability gap with the server version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "idp/ldap/list-access-keys-bulk", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringValue("ldap"),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolNull(),
			IncludePolicies:        types.BoolNull(),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})

		assertErrorDiagnostic(t, resp.Diagnostics,
			"Temporary account listing is not available on this RustFS server",
			"RustFS 1.0.3")
	})

	t.Run("reports a server failure", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method:       http.MethodGet,
			pathContains: "idp/ldap/list-access-keys-bulk",
			status:       http.StatusInternalServerError,
			body:         s3ErrorXML("InternalError", "iam not init"),
		})
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringValue("ldap"),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolNull(),
			IncludePolicies:        types.BoolNull(),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error listing temporary accounts", "iam not init")
	})

	t.Run("reports a capability gap for the policy lookup", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "idp/ldap/list-access-keys-bulk", status: http.StatusOK, body: temporaryAccountsLDAPPayload},
			requestRoute{method: http.MethodGet, pathContains: "temporary-account-info", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringValue("ldap"),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolNull(),
			IncludePolicies:        types.BoolValue(true),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})

		assertErrorDiagnostic(t, resp.Diagnostics,
			"Temporary account information is not available on this RustFS server",
			"include_policies = false")
	})

	t.Run("reports a failed service account policy lookup", func(t *testing.T) {
		const serviceAccountPayload = `{"dave":{"serviceAccounts":[` +
			`{"parentUser":"dave","accountStatus":"enabled","impliedPolicy":false,` +
			`"accessKey":"svc-key-1","name":null,"description":null,"expiration":null}],` +
			`"stsKeys":[]}}`
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "idp/ldap/list-access-keys-bulk", status: http.StatusOK, body: serviceAccountPayload},
			requestRoute{method: http.MethodGet, pathContains: "info-service-account", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "access key not exist")},
		)
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringValue("ldap"),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolValue(true),
			IncludePolicies:        types.BoolValue(true),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})

		assertErrorDiagnostic(t, resp.Diagnostics,
			"Error reading temporary account information",
			`access key "svc-key-1"`)
	})

	t.Run("reports a key revoked during the policy lookup", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "idp/ldap/list-access-keys-bulk", status: http.StatusOK, body: temporaryAccountsLDAPPayload},
			requestRoute{method: http.MethodGet, pathContains: "temporary-account-info", status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "temp account 'ldap-key-1' does not exist")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringValue("ldap"),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolNull(),
			IncludePolicies:        types.BoolValue(true),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})

		assertErrorDiagnostic(t, resp.Diagnostics,
			"Temporary account disappeared during the read",
			`Access key "ldap-key-1"`)
	})

	t.Run("reports a failed policy lookup", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "idp/ldap/list-access-keys-bulk", status: http.StatusOK, body: temporaryAccountsLDAPPayload},
			requestRoute{method: http.MethodGet, pathContains: "temporary-account-info", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "policy db is unavailable")},
		)
		d := newTemporaryAccountsDataSource(client)
		sch := dataSourceSchemaOf(t, d)

		resp := readTemporaryAccounts(t, d, sch, TemporaryAccountsDataSourceModel{
			UserProvider:           types.StringValue("ldap"),
			ParentUser:             types.StringNull(),
			IncludeServiceAccounts: types.BoolNull(),
			IncludePolicies:        types.BoolValue(true),
			Accounts:               types.ListNull(temporaryAccountObjectType()),
		})

		assertErrorDiagnostic(t, resp.Diagnostics,
			"Error reading temporary account information",
			`access key "ldap-key-1"`)
	})
}

func recorderSaw(recorder *recordingServer, request string) bool {
	for _, got := range recorder.snapshot() {
		if got == request {
			return true
		}
	}
	return false
}

func newTemporaryAccountsDataSource(client *AllClient) *TemporaryAccountsDataSource {
	d := NewTemporaryAccountsDataSource().(*TemporaryAccountsDataSource)
	d.client = client
	return d
}

func readTemporaryAccounts(
	t *testing.T,
	d *TemporaryAccountsDataSource,
	sch datasourceschema.Schema,
	model TemporaryAccountsDataSourceModel,
) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{Config: dataSourceConfig(t, sch, model)}, resp)
	return resp
}

// TestAccTemporaryAccountsDataSource filters to a parent user no test creates,
// so the listing is empty and the check does not race a parallel STS fixture.
// It still exercises the bulk routes the data source reads.
func TestAccTemporaryAccountsDataSource(t *testing.T) {
	parent := fmt.Sprintf("tf-acc-temporary-accounts-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
data "rustfs_temporary_accounts" "filtered" {
  user_provider = "builtin"
  parent_user   = %q
}
`, parent),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_temporary_accounts.filtered", "accounts.#", "0"),
					resource.TestCheckResourceAttr("data.rustfs_temporary_accounts.filtered", "user_provider", "builtin"),
					resource.TestCheckResourceAttr("data.rustfs_temporary_accounts.filtered", "parent_user", parent),
				),
			},
		},
	})
}

// TestAccTemporaryAccountsListAndRevoke proves the end-to-end STS surface: it
// mints a real temporary credential with AssumeRole, lists it with its policy,
// revokes it, and checks that the next listing no longer holds it. The unit
// tests mock the wire; this is the only test that proves the routes agree with
// a live RustFS.
func TestAccTemporaryAccountsListAndRevoke(t *testing.T) {
	parent := os.Getenv("RUSTFS_USER")
	dataSourceName := "data.rustfs_temporary_accounts.sts"
	resourceName := "rustfs_token_revocation.test"

	var accessKey string
	created := false
	assume := func() {
		if created {
			return
		}
		accessKey = testAccAssumeRole(t)
		created = true
	}

	t.Cleanup(func() {
		if !created {
			return
		}
		// Best effort: leave no temporary credential behind if a step failed
		// before the revocation resource ran.
		client := rustfs.New(&rustfs.RustfsAdminConfig{
			Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
			AccessKey:    os.Getenv("RUSTFS_USER"),
			AccessSecret: os.Getenv("RUSTFS_SECRET"),
		})
		_, _ = client.RevokeTokens(context.Background(), rustfs.UserProviderBuiltin, parent)
	})

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig: assume,
				Config: testAccProviderConfig() + fmt.Sprintf(`
data "rustfs_temporary_accounts" "sts" {
  user_provider    = "builtin"
  parent_user      = %q
  include_policies = true
}
`, parent),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckTemporaryAccountsContains(dataSourceName, &accessKey, true),
				),
			},
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_token_revocation" "test" {
  user_provider = "builtin"
  user          = %q

  triggers = {
    run = "1"
  }
}

data "rustfs_temporary_accounts" "sts" {
  user_provider = "builtin"
  parent_user   = %q
  depends_on    = [rustfs_token_revocation.test]
}
`, parent, parent),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckTokenRevocationCount(resourceName, 1),
					testAccCheckTemporaryAccountsAbsent(dataSourceName, &accessKey),
				),
			},
		},
	})
}

type testAccAssumeRoleResponse struct {
	Result struct {
		Credentials struct {
			AccessKeyID string `xml:"AccessKeyId"`
		} `xml:"Credentials"`
	} `xml:"AssumeRoleResult"`
}

// testAccAssumeRole mints one STS credential for the provider's own user. The
// request is signed directly because the provider does not ship an STS client.
func testAccAssumeRole(t *testing.T) string {
	t.Helper()

	body := "Action=AssumeRole&Version=2011-06-15&DurationSeconds=900"
	hashed := sha256.Sum256([]byte(body))
	payloadHash := hex.EncodeToString(hashed[:])

	// RootURL is the same composer the provider uses, so a scheme-carrying
	// RUSTFS_ENDPOINT does not become http://http://....
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		rustfs.RootURL(os.Getenv("RUSTFS_ENDPOINT"), false)+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the AssumeRole request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signer := v4.NewSigner()
	err = signer.SignHTTP(req.Context(), aws.Credentials{
		AccessKeyID:     os.Getenv("RUSTFS_USER"),
		SecretAccessKey: os.Getenv("RUSTFS_SECRET"),
	}, req, payloadHash, "s3", "us-east-01", time.Now().UTC())
	if err != nil {
		t.Fatalf("signing the AssumeRole request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("calling AssumeRole: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the AssumeRole answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AssumeRole answered %d: %s", resp.StatusCode, raw)
	}

	var parsed testAccAssumeRoleResponse
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("decoding the AssumeRole answer: %v", err)
	}
	if parsed.Result.Credentials.AccessKeyID == "" {
		t.Fatalf("AssumeRole returned no access key: %s", raw)
	}
	return parsed.Result.Credentials.AccessKeyID
}

func testAccCheckTemporaryAccountsContains(name string, accessKey *string, wantPolicy bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("not found: %s", name)
		}

		for i := 0; i < temporaryAccountsCount(rs.Primary.Attributes); i++ {
			prefix := fmt.Sprintf("accounts.%d.", i)
			if rs.Primary.Attributes[prefix+"access_key"] != *accessKey {
				continue
			}
			if wantPolicy {
				if rs.Primary.Attributes[prefix+"policy"] == "" {
					return fmt.Errorf("%s: account %s has no policy", name, *accessKey)
				}
				if _, ok := rs.Primary.Attributes[prefix+"implied_policy"]; !ok {
					return fmt.Errorf("%s: account %s has no implied_policy", name, *accessKey)
				}
			}
			return nil
		}
		return fmt.Errorf("%s does not list %s", name, *accessKey)
	}
}

func testAccCheckTemporaryAccountsAbsent(name string, accessKey *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("not found: %s", name)
		}

		for i := 0; i < temporaryAccountsCount(rs.Primary.Attributes); i++ {
			if rs.Primary.Attributes[fmt.Sprintf("accounts.%d.access_key", i)] == *accessKey {
				return fmt.Errorf("%s still lists %s", name, *accessKey)
			}
		}
		return nil
	}
}

func testAccCheckTokenRevocationCount(name string, minimum int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("not found: %s", name)
		}
		revoked, err := strconv.Atoi(rs.Primary.Attributes["revoked"])
		if err != nil {
			return fmt.Errorf("%s: revoked is not a number: %v", name, err)
		}
		if revoked < minimum {
			return fmt.Errorf("%s: revoked = %d, want at least %d", name, revoked, minimum)
		}
		return nil
	}
}

func temporaryAccountsCount(attributes map[string]string) int {
	count, err := strconv.Atoi(attributes["accounts.#"])
	if err != nil {
		return 0
	}
	return count
}

func TestTemporaryAccountsDataSourceEnrichPoliciesCancelled(t *testing.T) {
	d := newTemporaryAccountsDataSource(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp := &datasource.ReadResponse{}
	if d.enrichPolicies(ctx, []rustfs.TemporaryAccount{{AccessKey: "ldap-key-1"}}, resp) {
		t.Fatal("a cancelled context must stop the enrichment")
	}
	assertDiagnosticSummary(t, resp.Diagnostics, "Temporary account enrichment cancelled")
}

func TestTemporaryAccountsDataSourceEnrichPoliciesCancelledInFlight(t *testing.T) {
	var cancel context.CancelFunc
	client := &AllClient{RustClient: rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint: "127.0.0.1:1",
		HTTPClient: &http.Client{Transport: cancelDuringRoundTrip(func() {
			cancel()
		})},
	})}

	ctx, cancelFunc := context.WithCancel(context.Background())
	cancel = cancelFunc
	d := newTemporaryAccountsDataSource(client)

	resp := &datasource.ReadResponse{}
	if d.enrichPolicies(ctx, []rustfs.TemporaryAccount{{AccessKey: "ldap-key-1"}}, resp) {
		t.Fatal("a lookup cancelled in flight must stop the enrichment")
	}
	assertDiagnosticSummary(t, resp.Diagnostics, "Temporary account enrichment cancelled")
}

// cancelDuringRoundTrip cancels the request context while the lookup is in
// flight, which is the path a Terraform refresh takes when the user interrupts
// between keys.
type cancelDuringRoundTrip func()

func (f cancelDuringRoundTrip) RoundTrip(*http.Request) (*http.Response, error) {
	f()
	return nil, context.Canceled
}
