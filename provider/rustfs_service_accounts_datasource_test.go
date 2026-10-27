// Copyright (c) 2026 the terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// serviceAccountsTestPayload follows the RustFS 1.0.x list-service-accounts
// shape. The second entry carries the credential fields a conforming server
// never returns, so the test can prove they are dropped.
const serviceAccountsTestPayload = `{"accounts":[` +
	`{"parentUser":"u2","accountStatus":"on","impliedPolicy":true,"accessKey":"AK2",` +
	`"name":"two","description":"second","expiration":"2030-01-01T00:00:00Z"},` +
	`{"parentUser":"u1","accountStatus":"off","impliedPolicy":false,"accessKey":"AK1",` +
	`"name":"one","policy":"{\"Version\":\"2012-10-17\"}","secretKey":"leaked-secret"}` +
	`]}`

func newServiceAccountsDataSourceModel() *ServiceAccountsDataSourceModel {
	return &ServiceAccountsDataSourceModel{
		ID:       types.StringNull(),
		Accounts: types.ListNull(serviceAccountListObjectType()),
	}
}

func readServiceAccounts(t *testing.T, d *ServiceAccountsDataSource, sch datasourceschema.Schema) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, newServiceAccountsDataSourceModel()),
	}, resp)
	return resp
}

func TestServiceAccountsDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewServiceAccountsDataSource())

	for _, name := range []string{"id", "accounts"} {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
			t.Errorf("attribute %q must be Computed only", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}

	accounts, ok := sch.Attributes["accounts"].(datasourceschema.ListNestedAttribute)
	if !ok || !accounts.Computed {
		t.Fatalf("accounts must be a computed nested list: %+v", sch.Attributes["accounts"])
	}
	want := []string{
		"access_key", "name", "description", "expiration", "account_status",
		"parent_user", "implied_policy", "policy",
	}
	if len(accounts.NestedObject.Attributes) != len(want) {
		t.Errorf("expected %d nested attributes, got %v", len(want), accounts.NestedObject.Attributes)
	}
	for _, name := range want {
		attr := accounts.NestedObject.Attributes[name]
		if attr == nil || !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
			t.Errorf("accounts.%s must be Computed only: %+v", name, attr)
		}
		if attr.GetDescription() == "" {
			t.Errorf("nested attribute %q has no description", name)
		}
	}
	// A secret key attribute would be a leak by design; the schema must never
	// expose one, at the top level or in the listing.
	for name := range sch.Attributes {
		if strings.Contains(name, "secret") {
			t.Errorf("unexpected secret-carrying attribute %q", name)
		}
	}
	for name := range accounts.NestedObject.Attributes {
		if strings.Contains(name, "secret") {
			t.Errorf("unexpected secret-carrying nested attribute %q", name)
		}
	}
}

func TestServiceAccountsDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewServiceAccountsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_service_accounts" {
		t.Errorf("expected rustfs_service_accounts, got %s", resp.TypeName)
	}
}

func TestServiceAccountsDataSourceRead(t *testing.T) {
	client, recorder := routedClient(t,
		requestRoute{method: http.MethodGet, pathContains: "/rustfs/admin/v3/list-service-accounts", status: http.StatusOK, body: serviceAccountsTestPayload},
	)
	d := NewServiceAccountsDataSource().(*ServiceAccountsDataSource)
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	sch := dataSourceSchemaOf(t, d)

	resp := readServiceAccounts(t, d, sch)
	requireNoDiagnostics(t, resp.Diagnostics)
	if want := "GET /rustfs/admin/v3/list-service-accounts"; !recorderSaw(recorder, want) {
		t.Errorf("missing request %q in %v", want, recorder.requests)
	}

	var got ServiceAccountsDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ID.ValueString() == "" {
		t.Error("id must be set")
	}
	if rendered := fmt.Sprintf("%v", got.Accounts); strings.Contains(rendered, "leaked-secret") {
		t.Errorf("a secret reached the data source output: %s", rendered)
	}

	var accounts []serviceAccountListModel
	if diags := got.Accounts.ElementsAs(context.Background(), &accounts, false); diags.HasError() {
		t.Fatalf("reading the accounts: %v", diags)
	}
	if len(accounts) != 2 {
		t.Fatalf("expected two accounts, got %+v", accounts)
	}
	first := accounts[0]
	if first.AccessKey.ValueString() != "AK1" {
		t.Errorf("accounts must be sorted by access key, got %+v", accounts)
	}
	if first.ParentUser.ValueString() != "u1" || first.Name.ValueString() != "one" ||
		first.AccountStatus.ValueString() != "off" {
		t.Errorf("unexpected first account: %+v", first)
	}
	if first.ImpliedPolicy.ValueBool() {
		t.Error("implied_policy must be false for the first account")
	}
	if first.Policy.ValueString() != `{"Version":"2012-10-17"}` {
		t.Errorf("policy = %q", first.Policy.ValueString())
	}
	if !first.Description.IsNull() || !first.Expiration.IsNull() {
		t.Errorf("absent fields must be null: %+v", first)
	}

	second := accounts[1]
	if second.AccessKey.ValueString() != "AK2" || second.Description.ValueString() != "second" ||
		second.Expiration.ValueString() != "2030-01-01T00:00:00Z" || !second.ImpliedPolicy.ValueBool() {
		t.Errorf("unexpected second account: %+v", second)
	}
	if !second.Policy.IsNull() {
		t.Errorf("policy must be null when the account implies it: %+v", second.Policy)
	}
}

func TestServiceAccountsDataSourceReadEmpty(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{method: http.MethodGet, pathContains: "/rustfs/admin/v3/list-service-accounts", status: http.StatusOK, body: `{"accounts":[]}`},
	)
	d := NewServiceAccountsDataSource().(*ServiceAccountsDataSource)
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	sch := dataSourceSchemaOf(t, d)

	resp := readServiceAccounts(t, d, sch)
	requireNoDiagnostics(t, resp.Diagnostics)

	var got ServiceAccountsDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if elements := got.Accounts.Elements(); len(elements) != 0 {
		t.Errorf("expected no accounts, got %v", elements)
	}
}

func TestServiceAccountsDataSourceReadUnsupported(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{
			method:       http.MethodGet,
			pathContains: "/rustfs/admin/v3/list-service-accounts",
			status:       http.StatusNotImplemented,
			body:         s3ErrorXML("NotImplemented", "not implemented"),
		},
		requestRoute{
			method:       http.MethodGet,
			pathContains: "healthinfo",
			status:       http.StatusOK,
			body:         `{"version":"refs/tags/1.0.0"}`,
		},
	)
	d := NewServiceAccountsDataSource().(*ServiceAccountsDataSource)
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	sch := dataSourceSchemaOf(t, d)

	resp := readServiceAccounts(t, d, sch)
	diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Service account listing is not available on this RustFS server")
	if !strings.Contains(diagnostic.Detail(), "list-service-accounts") {
		t.Errorf("the diagnostic must name the route, got %q", diagnostic.Detail())
	}
}

// TestAccServiceAccountsDataSource creates a parent user with one service
// account and reads it back through the data source.
func TestAccServiceAccountsDataSource(t *testing.T) {
	testAccPreCheck(t)

	parentAccessKey := fmt.Sprintf("tf-test-sa-parent-%d", acctest.RandInt())
	accessKey := fmt.Sprintf("tf-test-sa-ds-%d", acctest.RandInt())
	name := fmt.Sprintf("tf-sa-%d", acctest.RandInt())
	resourceName := "rustfs_serviceaccount.test"

	config := testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_user" "parent" {
  access_key = %q
  secret_key = "parentSecret123!"
  status     = "enabled"
  policy     = ""
}

resource "rustfs_serviceaccount" "test" {
  access_key  = %q
  secret_key  = "superSecret123!"
  name        = %q
  description = "acceptance test service account"
  user        = rustfs_user.parent.access_key
}

data "rustfs_service_accounts" "all" {
  user       = rustfs_user.parent.access_key
  depends_on = [rustfs_serviceaccount.test]
}
`, parentAccessKey, accessKey, name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttrSet("data.rustfs_service_accounts.all", "id"),
					testAccCheckServiceAccountsContains("data.rustfs_service_accounts.all", resourceName),
				),
			},
		},
	})
}

// testAccCheckServiceAccountsContains finds the account the resource created in
// the listing by its access key and checks the parent user it is attributed to.
func testAccCheckServiceAccountsContains(dataSourceName, resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		accountResource, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("not found: %s", resourceName)
		}
		wantAccessKey := accountResource.Primary.Attributes["access_key"]
		if wantAccessKey == "" {
			return fmt.Errorf("%s has no access_key in state", resourceName)
		}
		wantParent := accountResource.Primary.Attributes["user"]

		dataSource, ok := s.RootModule().Resources[dataSourceName]
		if !ok {
			return fmt.Errorf("not found: %s", dataSourceName)
		}
		attrs := dataSource.Primary.Attributes
		count := 0
		if _, err := fmt.Sscanf(attrs["accounts.#"], "%d", &count); err != nil {
			return fmt.Errorf("reading accounts.# of %s: %w", dataSourceName, err)
		}
		for i := range count {
			prefix := fmt.Sprintf("accounts.%d.", i)
			if attrs[prefix+"access_key"] != wantAccessKey {
				continue
			}
			if wantParent != "" && attrs[prefix+"parent_user"] != wantParent {
				return fmt.Errorf("service account %s has parent_user %q, want %q",
					wantAccessKey, attrs[prefix+"parent_user"], wantParent)
			}
			return nil
		}
		return fmt.Errorf("service account %s is missing from the %s listing", wantAccessKey, dataSourceName)
	}
}
