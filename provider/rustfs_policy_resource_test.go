// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccPolicyResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-policy-%d", acctest.RandInt())
	resourceName := "rustfs_policy.test"

	const (
		originalAction   = "s3:GetObject"
		originalResource = "arn:aws:s3:::app-data"
		updatedAction    = "s3:PutObject"
		updatedResource  = "arn:aws:s3:::app-archive"
	)

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckPolicyDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPolicyConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPolicyExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", name),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
			{
				// The importer reads the object back and, having found nothing,
				// reports the missing object; the exact wording is Terraform
				// core's.
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: name + "-does-not-exist",
				ExpectError:   regexp.MustCompile(`(?i)(Cannot import non-existent remote object|no object exists with the given id)`),
			},
			{
				// In-place update: statement fields change without any
				// replacement, and the changed document is read back from the
				// server rather than only from the local state.
				Config: testAccPolicyConfigActionResource(name, updatedAction, updatedResource),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPolicyExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "statement.0.action.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.action.0", updatedAction),
					resource.TestCheckResourceAttr(resourceName, "statement.0.resource.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.resource.0", updatedResource),
					testAccCheckPolicyDocument(resourceName, "Allow", updatedAction, updatedResource),
				),
			},
			{
				// Revert the update; the server must carry the original values
				// again.
				Config: testAccPolicyConfigActionResource(name, originalAction, originalResource),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "statement.0.action.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.action.0", originalAction),
					resource.TestCheckResourceAttr(resourceName, "statement.0.resource.0", originalResource),
					testAccCheckPolicyDocument(resourceName, "Allow", originalAction, originalResource),
				),
			},
			{
				// Computed consistency: with the state matching this config the
				// post-apply plan must be explicitly empty.
				Config:   testAccPolicyConfigActionResource(name, originalAction, originalResource),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Drift: the policy is removed through the admin API while
				// Terraform still holds it in state, so the refresh must drop
				// it and the next plan must recreate it. Plan #1 does not
				// refresh and therefore cannot see the deletion; plan #2 is the
				// refreshed one, and it is the plan whose action is asserted.
				Config:             testAccPolicyConfigActionResource(name, originalAction, originalResource),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPolicyExists(resourceName),
					testAccDeletePolicyOutOfBand(t, name),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
			},
			{
				// Converge: the drifted resource is created again and the
				// document on the server matches the configuration.
				Config: testAccPolicyConfigActionResource(name, originalAction, originalResource),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPolicyExists(resourceName),
					testAccCheckPolicyDocument(resourceName, "Allow", originalAction, originalResource),
				),
			},
		},
	})
}

func testAccPolicyConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect    = "Allow"
    action    = ["s3:GetObject", "s3:ListBucket"]
    resource = ["arn:aws:s3:::*"]
  }]
}
`, name)
}

// testAccPolicyConfigActionResource builds a single-statement policy with one
// action and one resource, so both can be changed in place without depending on
// set ordering.
func testAccPolicyConfigActionResource(name, action, resourceARN string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    action   = ["%s"]
    resource = ["%s"]
  }]
}
`, name, action, resourceARN)
}

// testAccCheckPolicyDocument reads the policy from the server and asserts the
// single statement it carries, which is what proves an update reached RustFS
// instead of only the Terraform state.
func testAccCheckPolicyDocument(n, effect, action, resourceARN string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		policyName := rs.Primary.Attributes["name"]
		if policyName == "" {
			return fmt.Errorf("no policy name set")
		}
		client := testAccPolicyClient()
		policy, err := client.ReadPolicy(context.Background(), policyName)
		if err != nil {
			return fmt.Errorf("policy not found: %s", err)
		}
		if len(policy.Statement) != 1 {
			return fmt.Errorf("policy %s has %d statements, want 1", policyName, len(policy.Statement))
		}
		statement := policy.Statement[0]
		if statement.Effect != effect {
			return fmt.Errorf("policy %s effect is %q, want %q", policyName, statement.Effect, effect)
		}
		if len(statement.Action) != 1 || statement.Action[0] != action {
			return fmt.Errorf("policy %s action is %v, want [%s]", policyName, statement.Action, action)
		}
		if len(statement.Resource) != 1 || statement.Resource[0] != resourceARN {
			return fmt.Errorf("policy %s resource is %v, want [%s]", policyName, statement.Resource, resourceARN)
		}
		return nil
	}
}

// testAccCheckPolicyCondition reads the policy from the server and asserts the
// condition values of the first statement.
func testAccCheckPolicyCondition(n, operator, key, value string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		policyName := rs.Primary.Attributes["name"]
		if policyName == "" {
			return fmt.Errorf("no policy name set")
		}
		client := testAccPolicyClient()
		policy, err := client.ReadPolicy(context.Background(), policyName)
		if err != nil {
			return fmt.Errorf("policy not found: %s", err)
		}
		if len(policy.Statement) == 0 {
			return fmt.Errorf("policy %s has no statements", policyName)
		}
		values := policy.Statement[0].Condition[operator][key]
		if len(values) != 1 || values[0] != value {
			return fmt.Errorf("policy %s condition %s.%s is %v, want [%s]", policyName, operator, key, values, value)
		}
		return nil
	}
}

// testAccDeletePolicyOutOfBand removes the policy through the admin API, behind
// Terraform's back, so the following refresh finds a policy that no longer
// exists.
func testAccDeletePolicyOutOfBand(t *testing.T, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		t.Helper()
		client := testAccPolicyClient()
		if err := client.DeletePolicy(context.Background(), name); err != nil {
			return fmt.Errorf("could not delete policy %s out of band: %s", name, err)
		}
		t.Logf("deleted policy %s out of band", name)
		return nil
	}
}

func testAccCheckPolicyExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		policyName := rs.Primary.Attributes["name"]
		if policyName == "" {
			return fmt.Errorf("no policy name set")
		}
		client := testAccPolicyClient()
		_, err := client.ReadPolicy(context.Background(), policyName)
		if err != nil {
			return fmt.Errorf("policy not found: %s", err)
		}
		return nil
	}
}

func testAccCheckPolicyDestroy(s *terraform.State) error {
	client := testAccPolicyClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_policy" {
			continue
		}
		policyName := rs.Primary.Attributes["name"]
		if policyName == "" {
			continue
		}
		_, err := client.ReadPolicy(context.Background(), policyName)
		if err == nil {
			return fmt.Errorf("policy %s still exists", policyName)
		}
	}
	return nil
}

func testAccPolicyClient() rustfs.RustfsAdmin {
	return rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}

func TestPolicyResourceSchema(t *testing.T) {
	r := NewPolicyResource()
	resp := &frameworkresource.SchemaResponse{}
	r.Schema(context.TODO(), frameworkresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{"name", "version", "statement"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %q attribute", name)
		}
	}

	statement, ok := attrs["statement"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("statement attribute is not a ListNestedAttribute")
	}

	nested := statement.NestedObject.Attributes
	for _, name := range []string{"effect", "sid", "action", "not_action", "resource", "not_resource", "condition"} {
		if _, ok := nested[name]; !ok {
			t.Errorf("expected nested %q attribute", name)
		}
	}

	action, ok := nested["action"].(schema.SetAttribute)
	if !ok {
		t.Fatalf("action attribute is not a SetAttribute")
	}
	if !action.Optional || action.Required {
		t.Errorf("action must be optional, got optional=%t required=%t", action.Optional, action.Required)
	}

	notAction, ok := nested["not_action"].(schema.SetAttribute)
	if !ok {
		t.Fatalf("not_action attribute is not a SetAttribute")
	}
	if !notAction.Optional || notAction.Required {
		t.Errorf("not_action must be optional, got optional=%t required=%t", notAction.Optional, notAction.Required)
	}

	condition, ok := nested["condition"].(schema.MapAttribute)
	if !ok {
		t.Fatalf("condition attribute is not a MapAttribute")
	}
	wantConditionType := types.MapType{ElemType: types.ListType{ElemType: types.StringType}}
	if !condition.ElementType.Equal(wantConditionType) {
		t.Errorf("condition element type is %s, want %s", condition.ElementType, wantConditionType)
	}

	if len(statement.NestedObject.Validators) != 1 {
		t.Errorf("expected one statement validator, got %d", len(statement.NestedObject.Validators))
	}
}

func TestPolicyResourceMetadata(t *testing.T) {
	r := NewPolicyResource()
	resp := &frameworkresource.MetadataResponse{}
	r.Metadata(context.TODO(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_policy" {
		t.Errorf("expected rustfs_policy, got %s", resp.TypeName)
	}
}

func TestPolicyStatementValidator(t *testing.T) {
	statementType := map[string]attr.Type{
		"action":     types.SetType{ElemType: types.StringType},
		"not_action": types.SetType{ElemType: types.StringType},
	}
	statement := func(action, notAction attr.Value) types.Object {
		return types.ObjectValueMust(statementType, map[string]attr.Value{
			"action":     action,
			"not_action": notAction,
		})
	}

	tests := map[string]struct {
		object  types.Object
		wantErr bool
	}{
		"action only": {
			object: statement(types.SetValueMust(types.StringType, []attr.Value{types.StringValue("s3:GetObject")}), types.SetNull(types.StringType)),
		},
		"not_action only": {
			object: statement(types.SetNull(types.StringType), types.SetValueMust(types.StringType, []attr.Value{types.StringValue("sts:AssumeRole")})),
		},
		"both": {
			object: statement(types.SetValueMust(types.StringType, []attr.Value{types.StringValue("s3:GetObject")}), types.SetValueMust(types.StringType, []attr.Value{types.StringValue("sts:AssumeRole")})),
		},
		"neither": {
			object:  statement(types.SetNull(types.StringType), types.SetNull(types.StringType)),
			wantErr: true,
		},
		"unknown action defers": {
			object: statement(types.SetUnknown(types.StringType), types.SetNull(types.StringType)),
		},
		"unknown not_action defers": {
			object: statement(types.SetNull(types.StringType), types.SetUnknown(types.StringType)),
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			resp := &validator.ObjectResponse{}
			policyStatementValidator{}.ValidateObject(context.TODO(), validator.ObjectRequest{
				ConfigValue: testCase.object,
				Path:        path.Root("statement").AtListIndex(0),
			}, resp)

			if got := resp.Diagnostics.HasError(); got != testCase.wantErr {
				t.Fatalf("error = %t, want %t: %v", got, testCase.wantErr, resp.Diagnostics)
			}
			if !testCase.wantErr {
				return
			}

			withPath, ok := resp.Diagnostics[0].(diag.DiagnosticWithPath)
			if !ok {
				t.Fatalf("diagnostic %T does not carry a path", resp.Diagnostics[0])
			}
			wantPath := path.Root("statement").AtListIndex(0).AtName("not_action")
			if !withPath.Path().Equal(wantPath) {
				t.Errorf("diagnostic path is %s, want %s", withPath.Path(), wantPath)
			}
		})
	}
}

func TestPolicyStatementValidatorDescription(t *testing.T) {
	validator := policyStatementValidator{}

	description := validator.Description(context.TODO())
	if description == "" {
		t.Fatal("Description is empty")
	}
	if got := validator.MarkdownDescription(context.TODO()); got != description {
		t.Errorf("MarkdownDescription = %q, want the Description %q", got, description)
	}
}

func TestPolicyStatementsRoundTrip(t *testing.T) {
	models := []policyStatementModel{
		{
			Effect:   "Allow",
			Sid:      types.StringValue("PrefixAccess"),
			Action:   []string{"s3:ListBucket"},
			Resource: []string{"arn:aws:s3:::app-data"},
			Condition: map[string]map[string][]string{
				"StringLike": {"s3:prefix": {"tenant-42/*"}},
			},
		},
		{
			Effect:      "Deny",
			Sid:         types.StringNull(),
			NotAction:   []string{"sts:AssumeRole"},
			NotResource: []string{"arn:aws:s3:::other"},
		},
	}

	statements := policyStatementsFromModel(models)
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(statements))
	}
	if statements[0].Sid != "PrefixAccess" {
		t.Errorf("expected Sid PrefixAccess, got %q", statements[0].Sid)
	}
	if statements[1].Sid != "" {
		t.Errorf("expected empty Sid, got %q", statements[1].Sid)
	}
	if statements[1].Action != nil || statements[1].Resource != nil {
		t.Errorf("expected no Action/Resource, got %v / %v", statements[1].Action, statements[1].Resource)
	}
	wantCondition := rustfs.PolicyCondition{"StringLike": {"s3:prefix": {"tenant-42/*"}}}
	if !reflect.DeepEqual(statements[0].Condition, wantCondition) {
		t.Errorf("wrong condition: %v", statements[0].Condition)
	}

	back := policyStatementModelsFromAPI(statements)
	if !reflect.DeepEqual(back, models) {
		t.Errorf("round trip mismatch:\n got %#v\nwant %#v", back, models)
	}
}

func testAccPolicyConditionConfig(name string) string {
	return testAccPolicyConditionConfigPrefix(name, "tenant-42/*")
}

// testAccPolicyConditionConfigPrefix is the condition policy with a settable
// s3:prefix condition value so the condition block can be changed in place.
func testAccPolicyConditionConfigPrefix(name, prefix string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_policy" "test" {
  name = "%s"
  statement = [
    {
      sid      = "PrefixAccess"
      effect   = "Allow"
      action   = ["s3:ListBucket"]
      resource = ["arn:aws:s3:::app-data"]

      condition = {
        StringLike = {
          "s3:prefix" = ["%s"]
        }
      }
    },
    {
      effect       = "Allow"
      not_action   = ["sts:AssumeRole"]
      not_resource = ["arn:aws:s3:::other"]
    },
  ]
}
`, name, prefix)
}

func testAccPolicyMissingActionConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    resource = ["arn:aws:s3:::app-data"]
  }]
}
`, name)
}

func TestAccPolicyResource_condition(t *testing.T) {
	name := fmt.Sprintf("tf-test-policy-cond-%d", acctest.RandInt())
	resourceName := "rustfs_policy.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckPolicyDestroy,
		Steps: []resource.TestStep{
			{
				// The validation error must fire before anything is created, and
				// the step has to run first: the testing framework destroys the
				// last configured resource, which a rejected config cannot do.
				Config:      testAccPolicyMissingActionConfig(fmt.Sprintf("tf-test-policy-invalid-%d", acctest.RandInt())),
				ExpectError: regexp.MustCompile("must set either action or not_action"),
			},
			{
				Config: testAccPolicyConditionConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPolicyExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", name),
					resource.TestCheckResourceAttr(resourceName, "statement.0.sid", "PrefixAccess"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.action.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.action.0", "s3:ListBucket"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.%", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.StringLike.%", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.StringLike.s3:prefix.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.StringLike.s3:prefix.0", "tenant-42/*"),
					resource.TestCheckResourceAttr(resourceName, "statement.1.not_action.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.1.not_action.0", "sts:AssumeRole"),
					resource.TestCheckResourceAttr(resourceName, "statement.1.not_resource.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.1.not_resource.0", "arn:aws:s3:::other"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
			{
				// In-place update of the condition block: only the
				// StringLike/s3:prefix value changes, and the server must hold
				// the new value.
				Config: testAccPolicyConditionConfigPrefix(name, "tenant-99/*"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPolicyExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.StringLike.s3:prefix.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.StringLike.s3:prefix.0", "tenant-99/*"),
					testAccCheckPolicyCondition(resourceName, "StringLike", "s3:prefix", "tenant-99/*"),
				),
			},
			{
				// Revert the condition and read the original value back from the
				// server.
				Config: testAccPolicyConditionConfigPrefix(name, "tenant-42/*"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.StringLike.s3:prefix.0", "tenant-42/*"),
					testAccCheckPolicyCondition(resourceName, "StringLike", "s3:prefix", "tenant-42/*"),
				),
			},
			{
				// Computed consistency: no plan once state matches the config.
				Config:   testAccPolicyConditionConfigPrefix(name, "tenant-42/*"),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestPolicyResourceLifecyclePaths covers the paths the acceptance suite cannot
// reach with a live server: a policy that disappeared behind Terraform's back, a
// delete that arrives after the policy is already gone, and every failing API
// call in Create, Read and Delete.
func TestPolicyResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	state := func(name string) *policyResourceModel {
		return &policyResourceModel{
			Name:      types.StringValue(name),
			Version:   types.StringValue("2012-10-17"),
			Statement: []policyStatementModel{},
		}
	}

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// RustFS answers 404 NoSuchResource for a policy it does not have.
			name:        "missing policy is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "policy 'tf-test' does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading policy",
			wantDetail:  "Could not read policy, unexpected error: ",
		},
		{
			// A 200 with an undecodable policy document is a real read failure:
			// the client cannot parse what the server returned.
			name:        "undecodable policy document is reported",
			routes:      []requestRoute{{status: http.StatusOK, body: `{"policy_name":"tf-test","policy":"not-json"}`}},
			wantSummary: "Error reading policy",
			wantDetail:  "invalid character",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewPolicyResource(), client)

			resp := h.read(state("tf-test"))

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
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
			// RustFS answers 200 for removing a policy that is already gone.
			name:   "already deleted policy is not an error",
			routes: []requestRoute{{status: http.StatusOK, body: ""}},
		},
		{
			// Structured not-found is treated as already gone as well.
			name:   "not-found response is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "policy 'tf-test' does not exist")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "delete exploded")}},
			wantSummary: "Error deleting policy",
			wantDetail:  "delete exploded",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewPolicyResource(), client)

			resp := h.delete(state("tf-test"))

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
		plan        *policyResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected payload reports the server error and writes no state",
			routes:      []requestRoute{{method: http.MethodPut, pathContains: "add-canned-policy", status: http.StatusBadRequest, body: s3ErrorXML("InvalidArgument", "invalid policy document")}},
			plan:        &policyResourceModel{Name: types.StringValue("tf-test"), Version: types.StringValue("2012-10-17"), Statement: []policyStatementModel{{Effect: "Allow", Action: []string{"s3:ListBucket"}}}},
			wantSummary: "Error creating policy",
			wantDetail:  "invalid policy document",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewPolicyResource(), client)

			resp := h.create(tc.plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewPolicyResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
