// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccServiceAccountResource_basic(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-sa-%d", acctest.RandInt())
	resourceName := "rustfs_serviceaccount.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountConfig(accessKey, "test-service"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        accessKey,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
				ImportStateVerifyIgnore:              []string{"secret_key"},
			},
			{
				// The import id is the bare access key, so there is no
				// composite-id separator to diagnose; an id that does not exist
				// on the server must still fail cleanly instead of leaving
				// half-filled state behind.
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: "no/such/account",
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
}

// TestAccServiceAccountResource_update exercises the in-place update path for
// every attribute that does not force a replacement (name, description,
// expiration): each change is asserted both in state and as read back from the
// server, then reverted. It also pins the computed-attribute consistency with
// an explicit empty plan and verifies import of the account afterwards.
func TestAccServiceAccountResource_update(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-sa-%d", acctest.RandInt())
	resourceName := "rustfs_serviceaccount.test"

	original := serviceAccountConfigValues{
		accessKey:   accessKey,
		name:        "original-name",
		description: "acceptance test service account",
	}
	renamed := original
	renamed.name = "updated-name"
	described := renamed
	described.description = "updated description"
	expiring := described
	expiring.expiration = "2032-05-06T07:08:09Z"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountResourceConfig(original),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", "original-name"),
					testAccCheckServiceAccountServerAttr(resourceName, "description", "acceptance test service account"),
				),
			},
			{
				Config: testAccServiceAccountResourceConfig(renamed),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", "updated-name"),
					testAccCheckServiceAccountServerAttr(resourceName, "name", "updated-name"),
				),
			},
			{
				// In-place update of a non-key attribute: the new value must be
				// visible both in state and on the server.
				Config: testAccServiceAccountResourceConfig(described),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "description", "updated description"),
					testAccCheckServiceAccountServerAttr(resourceName, "description", "updated description"),
				),
			},
			{
				// Revert the in-place change.
				Config: testAccServiceAccountResourceConfig(renamed),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "description", "acceptance test service account"),
					testAccCheckServiceAccountServerAttr(resourceName, "description", "acceptance test service account"),
				),
			},
			{
				// Explicit empty second plan: no drift, and every computed
				// attribute agrees with what the server reports.
				Config:             testAccServiceAccountResourceConfig(renamed),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// In-place update of the optional+computed expiration.
				Config: testAccServiceAccountResourceConfig(expiring),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "expiration", "2032-05-06T07:08:09Z"),
					testAccCheckServiceAccountServerAttr(resourceName, "expiration", "2032-05-06T07:08:09Z"),
				),
			},
			{
				// Revert by omitting the attribute, which falls back to the
				// schema default.
				Config: testAccServiceAccountResourceConfig(described),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "expiration", "9999-01-01T00:00:00Z"),
					testAccCheckServiceAccountServerAttr(resourceName, "expiration", "9999-01-01T00:00:00Z"),
				),
			},
			{
				Config:             testAccServiceAccountResourceConfig(described),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        accessKey,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
				ImportStateVerifyIgnore:              []string{"secret_key"},
			},
		},
	})
}

// TestAccServiceAccountResource_replace drives the replacement path for every
// attribute carrying RequiresReplace(): `user` and `policy`. Each one gets a
// plan-only step asserting ResourceActionReplace and an applying step that
// asserts the new instance, followed by an explicit empty plan.
func TestAccServiceAccountResource_replace(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-sa-%d", acctest.RandInt())
	userAccessKey := fmt.Sprintf("tf-test-sauser-%d", acctest.RandInt())
	resourceName := "rustfs_serviceaccount.test"
	policyDoc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::*"]}]}`

	// The target user has to exist on the server before the account can be
	// scoped to it; it is created out of band so this test stays independent of
	// the rustfs_user resource.
	createTargetUser := func(s *terraform.State) error {
		client := testAccSAClient()
		return client.CreateUserAccount(context.Background(), rustfs.UserAccount{AccessKey: userAccessKey, SecretKey: "userSecret123!"})
	}
	deleteTargetUser := func(s *terraform.State) error {
		client := testAccSAClient()
		err := client.DeleteUserAccount(context.Background(), rustfs.UserAccount{AccessKey: userAccessKey})
		if err != nil && !rustfs.IsAPIErrorCode(err, "NoSuchResource") {
			return fmt.Errorf("could not clean up target user %s: %w", userAccessKey, err)
		}
		return nil
	}

	unscoped := serviceAccountConfigValues{
		accessKey:   accessKey,
		name:        "replace-account",
		description: "replacement test",
	}
	scoped := unscoped
	scoped.user = userAccessKey
	policied := scoped
	policied.policy = policyDoc

	replacePlan := resource.ConfigPlanChecks{
		PostApplyPreRefresh: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
		},
	}

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		// Destroy the account first, then the target user it was scoped to.
		CheckDestroy: func(s *terraform.State) error {
			if err := testAccCheckServiceAccountDestroy(s); err != nil {
				return err
			}
			return deleteTargetUser(s)
		},
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountResourceConfig(unscoped),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					createTargetUser,
				),
			},
			{
				// Setting `user` forces a replacement rather than an in-place
				// update.
				Config:             testAccServiceAccountResourceConfig(scoped),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks:   replacePlan,
			},
			{
				// Apply the replacement so the path actually runs, then assert
				// the new instance.
				Config: testAccServiceAccountResourceConfig(scoped),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "user", userAccessKey),
					testAccCheckServiceAccountServerAttr(resourceName, "parent_user", userAccessKey),
				),
			},
			{
				// Setting `policy` forces a replacement too.
				Config:             testAccServiceAccountResourceConfig(policied),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks:   replacePlan,
			},
			{
				Config: testAccServiceAccountResourceConfig(policied),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "policy", policyDoc),
					resource.TestCheckResourceAttr(resourceName, "implied_policy", "false"),
					testAccCheckServiceAccountServerPolicy(resourceName, policyDoc),
					testAccCheckServiceAccountServerImpliedPolicy(resourceName, false),
				),
			},
			{
				// Explicit empty plan after a replacement: the recreated
				// instance must be stable.
				Config:             testAccServiceAccountResourceConfig(policied),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccServiceAccountResource_drift deletes the account behind Terraform's
// back and asserts that the post-apply refresh plan reports the resource as
// missing (proposing a create) instead of surfacing a hard read error; the
// following step converges by recreating it.
func TestAccServiceAccountResource_drift(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-sa-%d", acctest.RandInt())
	resourceName := "rustfs_serviceaccount.test"
	cfg := testAccServiceAccountResourceConfig(serviceAccountConfigValues{
		accessKey:   accessKey,
		name:        "drift-account",
		description: "out-of-band drift test",
	})

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					testAccCheckServiceAccountServerAttr(resourceName, "name", "drift-account"),
				),
			},
			{
				// The Check runs after the apply, so the out-of-band deletion is
				// what the post-apply plans below observe.
				Config:             cfg,
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					testAccDeleteServiceAccountOutOfBand(resourceName),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
			},
			{
				// Converges: Terraform recreates the deleted account.
				Config: cfg,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					testAccCheckServiceAccountServerAttr(resourceName, "name", "drift-account"),
				),
			},
		},
	})
}

func testAccServiceAccountConfig(accessKey, name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_serviceaccount" "test" {
  access_key  = "%s"
  secret_key  = "superSecret123!"
  name        = "%s"
  description = "acceptance test service account"
}
`, accessKey, name)
}

// serviceAccountConfigValues describes a rustfs_serviceaccount config; empty
// optional values are omitted so the provider defaults apply.
type serviceAccountConfigValues struct {
	accessKey   string
	name        string
	description string
	expiration  string
	policy      string
	user        string
}

func testAccServiceAccountResourceConfig(v serviceAccountConfigValues) string {
	var b strings.Builder
	b.WriteString(testAccProviderConfig())
	b.WriteString("\nresource \"rustfs_serviceaccount\" \"test\" {\n")
	fmt.Fprintf(&b, "  access_key  = %q\n", v.accessKey)
	b.WriteString("  secret_key  = \"superSecret123!\"\n")
	fmt.Fprintf(&b, "  name        = %q\n", v.name)
	if v.description != "" {
		fmt.Fprintf(&b, "  description = %q\n", v.description)
	}
	if v.expiration != "" {
		fmt.Fprintf(&b, "  expiration  = %q\n", v.expiration)
	}
	if v.policy != "" {
		fmt.Fprintf(&b, "  policy      = %q\n", v.policy)
	}
	if v.user != "" {
		fmt.Fprintf(&b, "  user        = %q\n", v.user)
	}
	b.WriteString("}\n")
	return b.String()
}

func testAccCheckServiceAccountExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		accessKey := rs.Primary.Attributes["access_key"]
		if accessKey == "" {
			return fmt.Errorf("no access_key set")
		}
		client := testAccSAClient()
		_, err := client.ReadServiceAccount(context.Background(), accessKey)
		if err != nil {
			return fmt.Errorf("service account not found: %s", err)
		}
		return nil
	}
}

// testAccReadServiceAccount reads the account tracked by resource n straight
// from the admin API, i.e. bypassing Terraform state.
func testAccReadServiceAccount(n string, s *terraform.State) (rustfs.ServiceAccount, error) {
	rs, ok := s.RootModule().Resources[n]
	if !ok {
		return rustfs.ServiceAccount{}, fmt.Errorf("not found: %s", n)
	}
	accessKey := rs.Primary.Attributes["access_key"]
	if accessKey == "" {
		return rustfs.ServiceAccount{}, fmt.Errorf("no access_key set")
	}
	client := testAccSAClient()
	return client.ReadServiceAccount(context.Background(), accessKey)
}

// testAccCheckServiceAccountServerAttr asserts one attribute as the server
// itself reports it, so an in-place update cannot pass by only writing state.
func testAccCheckServiceAccountServerAttr(n, field, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		actual, err := testAccReadServiceAccount(n, s)
		if err != nil {
			return err
		}
		var got string
		switch field {
		case "name":
			got = actual.Name
		case "description":
			got = actual.Description
		case "expiration":
			got = actual.Expiration
		case "policy":
			got = actual.Policy
		case "parent_user":
			got = actual.ParentUser
		default:
			return fmt.Errorf("unsupported service account field %q", field)
		}
		if got != want {
			return fmt.Errorf("service account %s: server %s = %q, want %q", actual.ParentUser, field, got, want)
		}
		return nil
	}
}

func testAccCheckServiceAccountServerImpliedPolicy(n string, want bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		actual, err := testAccReadServiceAccount(n, s)
		if err != nil {
			return err
		}
		if actual.ImpliedPolicy != want {
			return fmt.Errorf("service account impliedPolicy = %v, want %v", actual.ImpliedPolicy, want)
		}
		return nil
	}
}

// testAccCheckServiceAccountServerPolicy asserts the IAM policy document the
// server reports. RustFS re-formats the stored document, so the comparison is
// by JSON value rather than by byte.
func testAccCheckServiceAccountServerPolicy(n, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		actual, err := testAccReadServiceAccount(n, s)
		if err != nil {
			return err
		}
		var gotDoc, wantDoc any
		if err := json.Unmarshal([]byte(actual.Policy), &gotDoc); err != nil {
			return fmt.Errorf("service account server policy is not valid JSON (%q): %w", actual.Policy, err)
		}
		if err := json.Unmarshal([]byte(want), &wantDoc); err != nil {
			return fmt.Errorf("expected policy is not valid JSON (%q): %w", want, err)
		}
		if !reflect.DeepEqual(gotDoc, wantDoc) {
			return fmt.Errorf("service account server policy = %q, want %q", actual.Policy, want)
		}
		if actual.ImpliedPolicy {
			return fmt.Errorf("service account server policy is set but impliedPolicy is true")
		}
		return nil
	}
}

// testAccDeleteServiceAccountOutOfBand deletes the account through the admin
// API, i.e. behind Terraform's back.
func testAccDeleteServiceAccountOutOfBand(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		accessKey := rs.Primary.Attributes["access_key"]
		if accessKey == "" {
			return fmt.Errorf("no access_key set")
		}
		client := testAccSAClient()
		if err := client.DeleteServiceAccount(context.Background(), rustfs.ServiceAccount{AccessKey: accessKey}); err != nil {
			return fmt.Errorf("out-of-band delete of service account %s: %w", accessKey, err)
		}
		if _, err := client.ReadServiceAccount(context.Background(), accessKey); err == nil {
			return fmt.Errorf("service account %s still readable after out-of-band delete", accessKey)
		}
		return nil
	}
}

func testAccCheckServiceAccountDestroy(s *terraform.State) error {
	client := testAccSAClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_serviceaccount" {
			continue
		}
		accessKey := rs.Primary.Attributes["access_key"]
		if accessKey == "" {
			continue
		}
		_, err := client.ReadServiceAccount(context.Background(), accessKey)
		if err == nil {
			return fmt.Errorf("service account %s still exists", accessKey)
		}
	}
	return nil
}

func testAccSAClient() rustfs.RustfsAdmin {
	return rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}

func TestAccServiceAccountExpiryPolicyResource(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-saexp-%d", acctest.RandInt())
	resourceName := "rustfs_serviceaccount.test"
	policyDoc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::*"]}]}`

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountExpiryPolicyConfig(accessKey, "2030-01-01T00:00:00Z", policyDoc),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
					resource.TestCheckResourceAttr(resourceName, "expiration", "2030-01-01T00:00:00Z"),
					resource.TestCheckResourceAttr(resourceName, "policy", policyDoc),
					resource.TestCheckResourceAttr(resourceName, "implied_policy", "false"),
				),
			},
			{
				Config: testAccServiceAccountExpiryPolicyConfig(accessKey, "2031-01-01T00:00:00Z", policyDoc),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "expiration", "2031-01-01T00:00:00Z"),
					testAccCheckServiceAccountServerAttr(resourceName, "expiration", "2031-01-01T00:00:00Z"),
				),
			},
		},
	})
}

func testAccServiceAccountExpiryPolicyConfig(accessKey, expiration, policy string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_serviceaccount" "test" {
  access_key  = "%s"
  secret_key  = "superSecret123!"
  name        = "expiryaccount"
  expiration  = "%s"
  policy      = %q
}
`, accessKey, expiration, policy)
}

// Migrated from main's read/delete/create error-path work.
var lifecycleSAAccessKey = unitTestCredential("RUSTFS_TEST_SA_ACCESS_KEY", 'k')
var lifecycleSASecretKey = unitTestCredential("RUSTFS_TEST_SA_SECRET_KEY", 'm')

func TestServiceAccountResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	// The base model every call starts from. Unset optional attributes use
	// typed nulls so tfsdk conversion keeps the attribute types.
	model := func() *serviceAccountResourceModel {
		return &serviceAccountResourceModel{
			AccessKey:     types.StringValue(lifecycleSAAccessKey),
			SecretKey:     types.StringValue(lifecycleSASecretKey),
			Name:          types.StringValue("tf-test sa"),
			Description:   types.StringNull(),
			TargetUser:    types.StringNull(),
			Expiration:    types.StringValue("9999-01-01T00:00:00Z"),
			Policy:        types.StringNull(),
			ImpliedPolicy: types.BoolNull(),
		}
	}

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantName    string
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: ReadServiceAccount answers
			// 404 NoSuchResource, message "service account '<ak>' does not exist".
			name:        "missing account is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "service account 'tf-test-sa' does not exist")}},
			wantRemoved: true,
		},
		{
			name:     "present account refreshes state",
			routes:   []requestRoute{{status: http.StatusOK, body: `{"accessKey":"tf-test-sa","name":"refreshed name","impliedPolicy":true}`}},
			wantName: "refreshed name",
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading service account",
			wantDetail:  "Could not read service account, unexpected error: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewServiceAccountResource(), client)

			resp := h.read(model())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("name"), tc.wantName)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: DeleteServiceAccount answers
			// 400 InvalidRequest with message "service account not exist".
			name:   "already deleted account is not an error",
			routes: []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "service account not exist")}},
		},
		{
			// The same status code with an unrelated message must still fail:
			// RustFS reuses InvalidRequest for many request failures.
			name:        "unrelated invalid request is still an error",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "access key is required")}},
			wantSummary: "Error deleting service account",
			wantDetail:  "Could not delete service account, unexpected error: ",
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting service account",
			wantDetail:  "Could not delete service account, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewServiceAccountResource(), client)

			resp := h.delete(model())

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "accepted account writes state",
			routes: []requestRoute{{method: http.MethodPut, pathContains: "add-service-accounts", status: http.StatusOK, body: `{"credentials":{"accessKey":"tf-test-sa","secretKey":"superSecret123!","expiration":"9999-01-01T00:00:00Z"}}`}},
		},
		{
			name:        "rejected account reports the server error and writes no state",
			routes:      []requestRoute{{method: http.MethodPut, pathContains: "add-service-accounts", status: http.StatusConflict, body: s3ErrorXML("InvalidRequest", "access key already exists")}},
			wantSummary: "Error creating service account",
			wantDetail:  "Could not create service account, unexpected error: ",
		},
		{
			name:        "forbidden create names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error creating service account",
			wantDetail:  "Could not create service account, unexpected error: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewServiceAccountResource(), client)
			plan := model()

			resp := h.create(plan)

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("access_key"), lifecycleSAAccessKey)
				// implied_policy is a bool, so assert it through the model
				// rather than the string helper.
				var created serviceAccountResourceModel
				if diags := resp.State.Get(context.Background(), &created); diags.HasError() {
					t.Fatalf("state get: %v", diags)
				}
				if !created.ImpliedPolicy.ValueBool() {
					t.Errorf("implied_policy = %v, want true for an account without a policy", created.ImpliedPolicy)
				}
				rec.assertRequest(t, "add-service-accounts")
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewServiceAccountResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
