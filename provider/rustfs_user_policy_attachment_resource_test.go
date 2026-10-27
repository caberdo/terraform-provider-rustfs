// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccUserPolicyAttachmentResource(t *testing.T) {
	userName := fmt.Sprintf("tf-test-upa-%d", acctest.RandInt())
	replacementUser := fmt.Sprintf("tf-test-upa-replacement-%d", acctest.RandInt())
	policyName := fmt.Sprintf("accpolicy-%d", acctest.RandInt())
	replacementPolicy := fmt.Sprintf("accpolicy-replacement-%d", acctest.RandInt())
	resourceName := "rustfs_user_policy_attachment.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUserPolicyAttachmentDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccUserPolicyAttachmentConfig(userName, policyName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user", userName),
					resource.TestCheckResourceAttr(resourceName, "policy", policyName),
					resource.TestCheckResourceAttr(resourceName, "id", userName+"/"+policyName),
					testAccCheckUserPolicyAttachmentAttached(resourceName),
					testAccCheckUserPolicies(userName, policyName),
				),
			},
			{
				// Computed consistency: an unchanged configuration plans
				// nothing, so the computed `id` agrees with the server.
				Config:   testAccUserPolicyAttachmentConfig(userName, policyName),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// `policy` carries RequiresReplace, so swapping it is a
				// replacement, never an in-place update.
				Config:             testAccUserPolicyAttachmentConfig(userName, replacementPolicy),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Applying the replacement really detaches the old policy and
				// attaches the new one: the server has to agree with the new
				// state.
				Config: testAccUserPolicyAttachmentConfig(userName, replacementPolicy),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "policy", replacementPolicy),
					resource.TestCheckResourceAttr(resourceName, "id", userName+"/"+replacementPolicy),
					testAccCheckUserPolicyAttachmentAttached(resourceName),
					testAccCheckUserPolicies(userName, replacementPolicy),
				),
			},
			{
				// Nothing left to plan after the replacement landed.
				Config:   testAccUserPolicyAttachmentConfig(userName, replacementPolicy),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateId:     userName + "/" + replacementPolicy,
				ImportStateVerify: true,
			},
			{
				// The composite ID is the only way in: anything else must fail
				// with the provider's own diagnostic instead of a half-filled
				// state.
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: "no-separator-here",
				ExpectError:   regexp.MustCompile(`(?s)Invalid import ID.*Expected format: <user>/<policy>`),
			},
			{
				// Revert: the replacement back to the original policy is a
				// replacement too.
				Config:             testAccUserPolicyAttachmentConfig(userName, policyName),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				Config: testAccUserPolicyAttachmentConfig(userName, policyName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "policy", policyName),
					resource.TestCheckResourceAttr(resourceName, "id", userName+"/"+policyName),
					testAccCheckUserPolicyAttachmentAttached(resourceName),
					testAccCheckUserPolicies(userName, policyName),
				),
			},
			{
				// `user` carries RequiresReplace as well, so moving the
				// attachment to another user replaces this resource.
				Config:             testAccUserPolicyAttachmentConfig(replacementUser, policyName),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// The replacement follows the user: the policy is attached to
				// the new user, and the old one is gone.
				Config: testAccUserPolicyAttachmentConfig(replacementUser, policyName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user", replacementUser),
					resource.TestCheckResourceAttr(resourceName, "policy", policyName),
					resource.TestCheckResourceAttr(resourceName, "id", replacementUser+"/"+policyName),
					testAccCheckUserPolicyAttachmentAttached(resourceName),
					testAccCheckUserPolicies(replacementUser, policyName),
					testAccCheckUserGone(userName),
				),
			},
			{
				Config:   testAccUserPolicyAttachmentConfig(replacementUser, policyName),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: the policy is detached behind Terraform's
				// back, so the attachment is dropped from state during the
				// refresh and the post-apply plan proposes to create it again.
				// The check runs after the apply, which is why this step
				// expects a non-empty plan.
				Config:             testAccUserPolicyAttachmentConfig(replacementUser, policyName),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserPolicyAttachmentAttached(resourceName),
					testAccCheckUserPolicies(replacementUser, policyName),
					testAccDetachUserPolicyOutOfBand(resourceName),
				),
			},
			{
				// The next apply converges back onto the configured attachment.
				Config: testAccUserPolicyAttachmentConfig(replacementUser, policyName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user", replacementUser),
					resource.TestCheckResourceAttr(resourceName, "policy", policyName),
					resource.TestCheckResourceAttr(resourceName, "id", replacementUser+"/"+policyName),
					testAccCheckUserPolicyAttachmentAttached(resourceName),
					testAccCheckUserPolicies(replacementUser, policyName),
				),
			},
			{
				Config:   testAccUserPolicyAttachmentConfig(replacementUser, policyName),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift on the user itself: removing the user takes
				// the attachment with it, so both are dropped from state during
				// the refresh and planned again.
				Config:             testAccUserPolicyAttachmentConfig(replacementUser, policyName),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserPolicyAttachmentAttached(resourceName),
					testAccRemoveUserOutOfBand(replacementUser, policyName),
				),
			},
			{
				// The next apply puts the user and its attachment back.
				Config: testAccUserPolicyAttachmentConfig(replacementUser, policyName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user", replacementUser),
					resource.TestCheckResourceAttr(resourceName, "policy", policyName),
					resource.TestCheckResourceAttr(resourceName, "id", replacementUser+"/"+policyName),
					testAccCheckUserPolicyAttachmentAttached(resourceName),
					testAccCheckUserPolicies(replacementUser, policyName),
				),
			},
			{
				Config:   testAccUserPolicyAttachmentConfig(replacementUser, policyName),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func testAccUserPolicyAttachmentConfig(userName, policyName string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect    = "Allow"
    action    = ["s3:GetObject"]
    resource  = ["arn:aws:s3:::accbucket/*"]
  }]
}
resource "rustfs_user" "test" {
  access_key = "%s"
  secret_key = "superSecret123!"
  policy     = rustfs_policy.test.name
}
resource "rustfs_user_policy_attachment" "test" {
  user   = rustfs_user.test.access_key
  policy = rustfs_policy.test.name
}
`, policyName, userName)
}

// testAccCheckUserPolicyAttachmentAttached asserts the attachment recorded in
// state is what the server reports for that user.
func testAccCheckUserPolicyAttachmentAttached(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %s not found in state", resourceName)
		}
		user := rs.Primary.Attributes["user"]
		policy := rs.Primary.Attributes["policy"]
		if user == "" || policy == "" {
			return fmt.Errorf("%s has an empty user or policy in state", resourceName)
		}
		attached, err := testAccUserPoliciesFromServer(user)
		if err != nil {
			return err
		}
		if !slices.Contains(attached, policy) {
			// The user is identified by its access key: name the fact, not it.
			return fmt.Errorf("the user the attachment is for does not carry policy %s (carries %v)", policy, attached)
		}
		return nil
	}
}

// testAccCheckUserPolicies asserts the complete policy list the server reports
// for a user, in order.
func testAccCheckUserPolicies(user string, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := testAccUserPoliciesFromServer(user)
		if err != nil {
			return err
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("the user does not carry the expected policies: got %v, want %v", got, want)
		}
		return nil
	}
}

// testAccCheckUserGone asserts a user was removed from the server.
func testAccCheckUserGone(user string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		_, err := client.ReadUserAccount(context.Background(), user)
		if err == nil {
			// user is the access key: report the fact, not the value.
			return fmt.Errorf("the user that should have been destroyed still exists")
		}
		if !rustfs.IsAPIErrorCode(err, "NoSuchResource") {
			return fmt.Errorf("could not determine whether the user still exists: %w", err)
		}
		return nil
	}
}

// testAccDetachUserPolicyOutOfBand removes the attachment behind Terraform's
// back, so the refreshed plan has to notice the drift.
func testAccDetachUserPolicyOutOfBand(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %s not found in state", resourceName)
		}
		user := rs.Primary.Attributes["user"]
		policy := rs.Primary.Attributes["policy"]
		client := testAccRustClient()
		if err := client.DetachUserPolicy(context.Background(), user, policy); err != nil {
			return fmt.Errorf("failed to detach policy %s from user %s: %w", policy, user, err)
		}
		return nil
	}
}

// testAccRemoveUserOutOfBand detaches the policy and then removes the user
// behind Terraform's back, so the attachment is gone with it.
func testAccRemoveUserOutOfBand(user, policy string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		if err := client.DetachUserPolicy(context.Background(), user, policy); err != nil {
			return fmt.Errorf("failed to detach policy %s from user %s: %w", policy, user, err)
		}
		if err := client.DeleteUserAccount(context.Background(), rustfs.UserAccount{AccessKey: user}); err != nil {
			return fmt.Errorf("failed to delete user %s: %w", user, err)
		}
		return nil
	}
}

// testAccUserPoliciesFromServer reads a user's whole policy list back from the
// admin API. RustFS stores it as one comma-separated string.
func testAccUserPoliciesFromServer(user string) ([]string, error) {
	client := testAccRustClient()
	info, err := client.ReadUserAccount(context.Background(), user)
	if err != nil {
		return nil, fmt.Errorf("failed to read user %s: %w", user, err)
	}
	policies := []string{}
	for _, name := range strings.Split(info.Policy, ",") {
		if name = strings.TrimSpace(name); name != "" {
			policies = append(policies, name)
		}
	}
	return policies, nil
}

// Both attributes of this resource carry RequiresReplace, so a change of
// `user` or `policy` is always planned as a replacement. Terraform applies a
// replacement as a destroy followed by a create, and the framework dispatches
// to Create whenever the apply arrives without a prior state - which is every
// replacement - so Update is unreachable from any plan and the acceptance
// steps above exercise Create instead. The method is still required by
// resource.Resource, so this drives it exactly the way the framework would (a
// plan plus a matching prior state) and asserts that it leaves the attachment
// on the server and records the state it planned, rather than dropping it.
func TestAccUserPolicyAttachmentResource_updateReattaches(t *testing.T) {
	testAccPreCheck(t)

	ctx := context.Background()
	suffix := acctest.RandInt()
	userName := fmt.Sprintf("tf-test-upa-update-%d", suffix)
	policyName := fmt.Sprintf("accpolicy-update-%d", suffix)

	client := testAccRustClient()
	if err := client.CreatePolicy(ctx, rustfs.Policy{
		Name: policyName,
		Statement: []rustfs.PolicyStatement{{
			Effect:   "Allow",
			Action:   []string{"s3:GetObject"},
			Resource: []string{"arn:aws:s3:::accbucket/*"},
		}},
	}); err != nil {
		t.Fatalf("failed to create policy %s: %v", policyName, err)
	}
	// The user is created without a policy, so the attachment only exists once
	// Update has run.
	if err := client.CreateUserAccount(ctx, rustfs.UserAccount{
		AccessKey: userName,
		SecretKey: "superSecret123!",
	}); err != nil {
		t.Fatalf("failed to create user %s: %v", userName, err)
	}
	t.Cleanup(func() {
		if err := client.DetachUserPolicy(ctx, userName, policyName); err != nil {
			t.Errorf("failed to detach %s from %s: %v", policyName, userName, err)
		}
		if err := client.DeleteUserAccount(ctx, rustfs.UserAccount{AccessKey: userName}); err != nil {
			t.Errorf("failed to delete user %s: %v", userName, err)
		}
		if err := client.DeletePolicy(ctx, policyName); err != nil {
			t.Errorf("failed to delete policy %s: %v", policyName, err)
		}
	})

	attachment := &UserPolicyAttachmentResource{client: &AllClient{RustClient: client}}

	var schemaResponse fwresource.SchemaResponse
	attachment.Schema(ctx, fwresource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("failed to build the resource schema: %v", schemaResponse.Diagnostics)
	}

	identifier := userName + "/" + policyName
	planValue := tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
		"user":   tftypes.NewValue(tftypes.String, userName),
		"policy": tftypes.NewValue(tftypes.String, policyName),
		"id":     tftypes.NewValue(tftypes.String, identifier),
	})

	updateResponse := &fwresource.UpdateResponse{
		State: tfsdk.State{Schema: schemaResponse.Schema, Raw: planValue},
	}
	attachment.Update(ctx, fwresource.UpdateRequest{
		Plan: tfsdk.Plan{Schema: schemaResponse.Schema, Raw: planValue},
	}, updateResponse)

	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("Update reported diagnostics: %v", updateResponse.Diagnostics)
	}

	var state UserPolicyAttachmentResourceModel
	if diags := updateResponse.State.Get(ctx, &state); diags.HasError() {
		t.Fatalf("failed to read the state Update recorded: %v", diags)
	}
	if state.User.ValueString() != userName || state.Policy.ValueString() != policyName || state.ID.ValueString() != identifier {
		t.Fatalf("Update recorded state %q/%q/%q, expected %q/%q/%q",
			state.User.ValueString(), state.Policy.ValueString(), state.ID.ValueString(),
			userName, policyName, identifier)
	}

	attached, err := testAccUserPoliciesFromServer(userName)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(attached, policyName) {
		t.Fatalf("user %s does not carry policy %q after Update (carries %v)", userName, policyName, attached)
	}
}

func testAccCheckUserPolicyAttachmentDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_user_policy_attachment" {
			continue
		}
		user := rs.Primary.Attributes["user"]
		if user == "" {
			continue
		}
		if _, err := client.ReadUserAccount(context.Background(), user); err == nil {
			return fmt.Errorf("user %s still exists", user)
		}
	}
	return nil
}

// Migrated from main's read/delete/create error-path work.
func TestUserPolicyAttachmentResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// RustFS answers 404 NoSuchResource for a user it does not have.
			name: "missing user is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "user-info",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "user 'tf-test' does not exist"),
			}},
			wantRemoved: true,
		},
		{
			// user-info returns the user's whole policy list in policyName, so
			// a policy missing from it was detached out-of-band.
			name: "policy no longer on the user is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "user-info",
				status: http.StatusOK, body: `{"policyName":"other-policy","memberOf":[],"status":"enabled"}`,
			}},
			wantRemoved: true,
		},
		{
			name: "still attached policy keeps state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "user-info",
				status: http.StatusOK, body: `{"policyName":"other-policy, tf-test-policy","memberOf":[],"status":"enabled"}`,
			}},
		},
		{
			name: "server error keeps state and names the failing call",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "user-info",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error reading user policy attachment",
			wantDetail:  "Could not read user " + lifecycleAttachmentUser + ": ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewUserPolicyAttachmentResource(), client)

			resp := h.read(&UserPolicyAttachmentResourceModel{
				User:   types.StringValue(lifecycleAttachmentUser),
				Policy: types.StringValue("tf-test-policy"),
				ID:     types.StringValue("tf-test/tf-test-policy"),
			})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertStatePresent(t, resp.State)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)
			assertStatePresent(t, resp.State)
			assertStateString(t, resp.State, path.Root("id"), lifecycleAttachmentUser+"/tf-test-policy")
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// The builtin detach endpoint answers a missing user with 400
			// InvalidArgument "user not exist" (probed against RustFS latest).
			name: "already deleted user is not an error",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusBadRequest, body: s3ErrorXML("InvalidArgument", "user not exist"),
			}},
		},
		{
			// A user that is gone cannot still carry the policy, so a
			// not-found from the read-style path is idempotent too.
			name: "not-found user is not an error",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "user 'tf-test' does not exist"),
			}},
		},
		{
			name: "real server error is reported",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error detaching policy from user",
			wantDetail:  "Could not detach policy: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewUserPolicyAttachmentResource(), client)

			resp := h.delete(&UserPolicyAttachmentResourceModel{
				User:   types.StringValue(lifecycleAttachmentUser),
				Policy: types.StringValue("tf-test-policy"),
				ID:     types.StringValue("tf-test/tf-test-policy"),
			})

			rec.assertRequest(t, "policy/detach")
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
		plan        *UserPolicyAttachmentResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			name: "rejected attach reports a diagnostic and writes no state",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/attach",
				status: http.StatusBadRequest, body: s3ErrorXML("InvalidArgument", "user not exist"),
			}},
			plan: &UserPolicyAttachmentResourceModel{
				User:   types.StringValue(lifecycleAttachmentUser),
				Policy: types.StringValue("tf-test-policy"),
				ID:     types.StringNull(),
			},
			wantSummary: "Error attaching policy to user",
			wantDetail:  "Could not attach policy: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewUserPolicyAttachmentResource(), client)

			resp := h.create(tc.plan)

			rec.assertRequest(t, "policy/attach")
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewUserPolicyAttachmentResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

var lifecycleAttachmentUser = unitTestCredential("RUSTFS_TEST_ATTACH_USER", 'p')
