// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
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

func TestAccGroupPolicyAttachmentResource_basic(t *testing.T) {
	groupName := fmt.Sprintf("tf-test-group-%d", acctest.RandInt())
	policyName := fmt.Sprintf("tf-test-policy-%d", acctest.RandInt())
	replacementPolicy := fmt.Sprintf("tf-test-policy-replacement-%d", acctest.RandInt())
	resourceName := "rustfs_group_policy_attachment.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckGroupPolicyAttachmentDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccGroupPolicyAttachmentConfig(groupName, policyName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "group", groupName),
					resource.TestCheckResourceAttr(resourceName, "policy", policyName),
					resource.TestCheckResourceAttr(resourceName, "id", groupName+"/"+policyName),
					testAccCheckGroupPolicyAttachmentExists(resourceName),
					testAccCheckGroupPolicies(groupName, policyName),
				),
			},
			// Both attributes carry RequiresReplace, so changing `policy` is
			// the only plan this resource can produce: a replacement, never an
			// in-place update.
			{
				Config:             testAccGroupPolicyAttachmentSwapConfig(groupName, policyName, replacementPolicy, "alt"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			// Applying that replacement re-attaches on the server. The new
			// policy is what the group carries; the old one is gone, and both
			// the state and the server agree on the new attachment.
			{
				Config: testAccGroupPolicyAttachmentSwapConfig(groupName, policyName, replacementPolicy, "alt"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "policy", replacementPolicy),
					resource.TestCheckResourceAttr(resourceName, "id", groupName+"/"+replacementPolicy),
					testAccCheckGroupPolicyAttachmentExists(resourceName),
					testAccCheckGroupPolicies(groupName, replacementPolicy),
				),
			},
			// Replace back: the attachment must end on the original policy
			// again, read back from the group.
			{
				Config: testAccGroupPolicyAttachmentSwapConfig(groupName, policyName, replacementPolicy, "test"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "policy", policyName),
					resource.TestCheckResourceAttr(resourceName, "id", groupName+"/"+policyName),
					testAccCheckGroupPolicyAttachmentExists(resourceName),
					testAccCheckGroupPolicies(groupName, policyName),
				),
			},
			// An unchanged configuration must plan nothing: the computed `id`
			// has to agree with the values the group actually carries.
			{
				Config:   testAccGroupPolicyAttachmentSwapConfig(groupName, policyName, replacementPolicy, "test"),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateId:     groupName + "/" + policyName,
				ImportStateVerify: true,
			},
			// The composite ID is the only way in; anything else must fail with
			// the provider's own diagnostic rather than a half-filled state.
			{
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: "no-separator-here",
				ExpectError:   regexp.MustCompile(`Expected format: <group>/<policy>`),
			},
		},
	})
}

// An attachment must add and remove exactly its own policy. This covers the
// three ways another binding can be lost: a second attachment in the same
// apply, a policy attached outside Terraform, and a detach performed
// out-of-band.
func TestAccGroupPolicyAttachmentResource_keepsOtherPolicies(t *testing.T) {
	groupName := fmt.Sprintf("tf-test-group-%d", acctest.RandInt())
	policyA := fmt.Sprintf("tf-test-policy-a-%d", acctest.RandInt())
	policyB := fmt.Sprintf("tf-test-policy-b-%d", acctest.RandInt())
	outOfBandPolicy := fmt.Sprintf("tf-test-policy-oob-%d", acctest.RandInt())
	secondGroup := fmt.Sprintf("tf-test-group-second-%d", acctest.RandInt())

	resourceA := "rustfs_group_policy_attachment.a"
	resourceB := "rustfs_group_policy_attachment.b"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			client := testAccRustClient()
			if err := client.CreatePolicy(context.Background(), rustfs.Policy{
				Name: outOfBandPolicy,
				Statement: []rustfs.PolicyStatement{{
					Effect:   "Allow",
					Action:   []string{"s3:ListAllMyBuckets"},
					Resource: []string{"arn:aws:s3:::*"},
				}},
			}); err != nil {
				t.Fatalf("failed to create out-of-band policy %s: %v", outOfBandPolicy, err)
			}
			t.Cleanup(func() {
				if err := client.DeletePolicy(context.Background(), outOfBandPolicy); err != nil {
					t.Errorf("failed to delete out-of-band policy %s: %v", outOfBandPolicy, err)
				}
			})
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckGroupPolicyAttachmentDestroy,
		Steps: []resource.TestStep{
			// One attachment, then a policy attached to the same group outside
			// Terraform.
			{
				Config: testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceA, "id", groupName+"/"+policyA),
					testAccCheckGroupPolicies(groupName, policyA),
					testAccAttachGroupPolicyOutOfBand(groupName, outOfBandPolicy),
				),
			},
			// The second attachment must keep the first one and the policy
			// attached outside Terraform.
			{
				Config: testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, policyB),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceB, "id", groupName+"/"+policyB),
					testAccCheckGroupPolicies(groupName, policyA, policyB, outOfBandPolicy),
				),
			},
			// A detach performed outside Terraform is detected by Read, so the
			// refreshed plan proposes to create the attachment again. The check
			// runs after the apply, which is why this step expects a non-empty
			// plan.
			{
				Config:             testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, policyB),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupPolicyAttachmentExists(resourceA),
					testAccCheckGroupPolicies(groupName, policyA, policyB, outOfBandPolicy),
					testAccDetachGroupPolicyOutOfBand(groupName, policyA),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceA, plancheck.ResourceActionCreate),
					},
				},
			},
			// The next apply recreates the attachment without touching the
			// other policies.
			{
				Config: testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, policyB),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceA, "id", groupName+"/"+policyA),
					testAccCheckGroupPolicyAttachmentExists(resourceA),
					testAccCheckGroupPolicies(groupName, policyA, policyB, outOfBandPolicy),
				),
			},
			// Nothing left to do once converged.
			{
				Config:   testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, policyB),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			// `group` carries RequiresReplace too, so moving the attachment to
			// another group is a replacement of that attachment, and of nothing
			// else.
			{
				Config:             testAccGroupPolicyAttachmentRegroupConfig(groupName, secondGroup, policyA, policyB),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceA, plancheck.ResourceActionReplace),
					},
				},
			},
			// Applying it must leave the new group carrying the policy, while
			// the original group keeps everything else.
			{
				Config: testAccGroupPolicyAttachmentRegroupConfig(groupName, secondGroup, policyA, policyB),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceA, "group", secondGroup),
					resource.TestCheckResourceAttr(resourceA, "id", secondGroup+"/"+policyA),
					testAccCheckGroupPolicyAttachmentExists(resourceA),
					testAccCheckGroupPolicies(secondGroup, policyA),
					testAccCheckGroupPolicies(groupName, policyB, outOfBandPolicy),
				),
			},
			{
				ResourceName:      resourceA,
				ImportState:       true,
				ImportStateId:     secondGroup + "/" + policyA,
				ImportStateVerify: true,
			},
			{
				ResourceName:  resourceA,
				ImportState:   true,
				ImportStateId: groupName,
				ExpectError:   regexp.MustCompile(`Expected format: <group>/<policy>`),
			},
		},
	})
}

// Both attributes of this resource carry RequiresReplace, so a change of
// `group` or `policy` is always planned as a replacement. Terraform applies a
// replacement as a destroy followed by a create, and the framework dispatches
// to Create whenever the apply arrives without a prior state - which is every
// replacement - so Update is unreachable from any plan and the acceptance steps
// above exercise Create instead. The method is still required by
// resource.Resource, so this drives it exactly the way the framework would (a
// plan plus a matching prior state) and asserts that it leaves the attachment
// on the server and records the state it planned, rather than dropping it.
func TestAccGroupPolicyAttachmentResource_updateReattaches(t *testing.T) {
	testAccPreCheck(t)

	ctx := context.Background()
	groupName := fmt.Sprintf("tf-test-group-%d", acctest.RandInt())
	policyName := fmt.Sprintf("tf-test-policy-%d", acctest.RandInt())

	client := testAccRustClient()
	// The admin API creates a group implicitly via update-group-members, so it
	// must be called even with no members.
	if err := client.UpdateGroupMembers(ctx, rustfs.GroupAddRemove{Group: groupName, Status: "enabled"}); err != nil {
		t.Fatalf("failed to create group %s: %v", groupName, err)
	}
	if err := client.CreatePolicy(ctx, rustfs.Policy{
		Name: policyName,
		Statement: []rustfs.PolicyStatement{{
			Effect:   "Allow",
			Action:   []string{"s3:ListAllMyBuckets"},
			Resource: []string{"arn:aws:s3:::*"},
		}},
	}); err != nil {
		t.Fatalf("failed to create policy %s: %v", policyName, err)
	}
	t.Cleanup(func() {
		if err := client.DetachGroupPolicy(ctx, groupName, policyName); err != nil {
			t.Errorf("failed to detach %s from %s: %v", policyName, groupName, err)
		}
		if err := client.DeleteGroup(ctx, groupName); err != nil {
			t.Errorf("failed to delete group %s: %v", groupName, err)
		}
		if err := client.DeletePolicy(ctx, policyName); err != nil {
			t.Errorf("failed to delete policy %s: %v", policyName, err)
		}
	})

	attachment := &GroupPolicyAttachmentResource{client: &AllClient{RustClient: client}}

	var schemaResponse fwresource.SchemaResponse
	attachment.Schema(ctx, fwresource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("failed to build the resource schema: %v", schemaResponse.Diagnostics)
	}

	identifier := groupName + "/" + policyName
	planValue := tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
		"group":  tftypes.NewValue(tftypes.String, groupName),
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

	var state GroupPolicyAttachmentResourceModel
	if diags := updateResponse.State.Get(ctx, &state); diags.HasError() {
		t.Fatalf("failed to read the state Update recorded: %v", diags)
	}
	if state.Group.ValueString() != groupName || state.Policy.ValueString() != policyName || state.ID.ValueString() != identifier {
		t.Fatalf("Update recorded state %q/%q/%q, expected %q/%q/%q",
			state.Group.ValueString(), state.Policy.ValueString(), state.ID.ValueString(),
			groupName, policyName, identifier)
	}

	info, err := client.GetGroup(ctx, groupName)
	if err != nil {
		t.Fatalf("failed to read group %s: %v", groupName, err)
	}
	if !slices.Contains(info.Policies(), policyName) {
		t.Fatalf("group %s does not carry policy %q after Update", groupName, policyName)
	}
}

func testAccGroupPolicyAttachmentConfig(groupName, policyName string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_group" "test" {
  name = "%s"
}

resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect    = "Allow"
    action    = ["s3:GetObject"]
    resource = ["arn:aws:s3:::%s/*"]
  }]
}

resource "rustfs_group_policy_attachment" "test" {
  group  = rustfs_group.test.name
  policy = rustfs_policy.test.name
}
`, groupName, policyName, groupName)
}

// testAccGroupPolicyAttachmentMultiConfig attaches policyB only when it is not
// empty, so the same test can step from one attachment to two.
func testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, policyB string) string {
	policyBBlock := ""
	attachmentB := ""
	if policyB != "" {
		policyBBlock = fmt.Sprintf(`
resource "rustfs_policy" "b" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    action   = ["s3:ListAllMyBuckets"]
    resource = ["arn:aws:s3:::*"]
  }]
}
`, policyB)
		attachmentB = `
resource "rustfs_group_policy_attachment" "b" {
  group  = rustfs_group.test.name
  policy = rustfs_policy.b.name
}
`
	}

	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_group" "test" {
  name = "%s"
}

resource "rustfs_policy" "a" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    action   = ["s3:GetBucketLocation"]
    resource = ["arn:aws:s3:::*"]
  }]
}
%s
resource "rustfs_group_policy_attachment" "a" {
  group  = rustfs_group.test.name
  policy = rustfs_policy.a.name
}
%s`, groupName, policyA, policyBBlock, attachmentB)
}

// testAccGroupPolicyAttachmentSwapConfig declares the original policy plus an
// alternative one and points the single attachment at whichever resource key is
// named by attachedPolicy ("test" or "alt"). Both policies stay in the
// configuration, so changing `policy` plans a replacement of the attachment
// alone and no policy is ever destroyed while it is still attached.
func testAccGroupPolicyAttachmentSwapConfig(groupName, policyName, altPolicy, attachedPolicy string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_group" "test" {
  name = "%s"
}

resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect    = "Allow"
    action    = ["s3:GetObject"]
    resource = ["arn:aws:s3:::%s/*"]
  }]
}

resource "rustfs_policy" "alt" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    action   = ["s3:ListAllMyBuckets"]
    resource = ["arn:aws:s3:::*"]
  }]
}

resource "rustfs_group_policy_attachment" "test" {
  group  = rustfs_group.test.name
  policy = rustfs_policy.%s.name
}
`, groupName, policyName, groupName, altPolicy, attachedPolicy)
}

// testAccGroupPolicyAttachmentRegroupConfig keeps every resource the multi
// config declares and adds a second group, moving attachment "a" onto it:
// changing `group` is a replacement of that attachment while attachment "b" and
// both policies are left alone.
func testAccGroupPolicyAttachmentRegroupConfig(groupName, secondGroup, policyA, policyB string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_group" "test" {
  name = "%s"
}

resource "rustfs_group" "second" {
  name = "%s"
}

resource "rustfs_policy" "a" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    action   = ["s3:GetBucketLocation"]
    resource = ["arn:aws:s3:::*"]
  }]
}

resource "rustfs_policy" "b" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    action   = ["s3:ListAllMyBuckets"]
    resource = ["arn:aws:s3:::*"]
  }]
}

resource "rustfs_group_policy_attachment" "a" {
  group  = rustfs_group.second.name
  policy = rustfs_policy.a.name
}

resource "rustfs_group_policy_attachment" "b" {
  group  = rustfs_group.test.name
  policy = rustfs_policy.b.name
}
`, groupName, secondGroup, policyA, policyB)
}

// testAccAttachGroupPolicyOutOfBand attaches a policy without Terraform, the
// way an operator would with the CLI, and returns an error if it fails.
func testAccAttachGroupPolicyOutOfBand(group, policy string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		if err := client.AttachGroupPolicy(context.Background(), group, policy); err != nil {
			return fmt.Errorf("failed to attach %s to %s outside Terraform: %w", policy, group, err)
		}
		return nil
	}
}

// testAccCheckGroupPolicyAttachmentExists reads the attachment back from the
// server: the resource must still be in state and the group it names must
// actually carry the policy it names. This is the post-apply read-back the
// create, replacement and drift-convergence paths must leave behind.
func testAccCheckGroupPolicyAttachmentExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("attachment %s not found in state", resourceName)
		}

		group := rs.Primary.Attributes["group"]
		policy := rs.Primary.Attributes["policy"]
		if group == "" || policy == "" {
			return fmt.Errorf("attachment %s has group %q and policy %q in state", resourceName, group, policy)
		}

		client := testAccRustClient()
		info, err := client.GetGroup(context.Background(), group)
		if err != nil {
			return fmt.Errorf("failed to read group %s: %w", group, err)
		}
		if !slices.Contains(info.Policies(), policy) {
			return fmt.Errorf("group %s does not carry policy %q on the server", group, policy)
		}
		return nil
	}
}

// testAccDetachGroupPolicyOutOfBand detaches a policy without Terraform, the
// way an operator would with the CLI, and returns an error if it fails.
func testAccDetachGroupPolicyOutOfBand(group, policy string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		if err := client.DetachGroupPolicy(context.Background(), group, policy); err != nil {
			return fmt.Errorf("failed to detach %s from %s outside Terraform: %w", policy, group, err)
		}
		return nil
	}
}

// testAccCheckGroupPolicies requires the group's policy list to be exactly the
// given set; a missing policy means an attachment was dropped, an extra one
// means a policy was left behind.
func testAccCheckGroupPolicies(group string, policies ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		info, err := client.GetGroup(context.Background(), group)
		if err != nil {
			return fmt.Errorf("failed to read group %s: %w", group, err)
		}

		attached := info.Policies()
		slices.Sort(attached)
		want := slices.Clone(policies)
		slices.Sort(want)

		if !slices.Equal(attached, want) {
			return fmt.Errorf("group %s holds policies %v, expected %v", group, attached, want)
		}
		return nil
	}
}

// Destruction is verified against the group's own policy mapping: after the
// attachment is removed the group must either be gone or no longer carry the
// policy, while any other policy it holds stays attached.
func testAccCheckGroupPolicyAttachmentDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_group_policy_attachment" {
			continue
		}
		group := rs.Primary.Attributes["group"]
		policy := rs.Primary.Attributes["policy"]
		if group == "" {
			continue
		}
		info, err := client.GetGroup(context.Background(), group)
		if err != nil {
			continue
		}
		if slices.Contains(info.Policies(), policy) {
			return fmt.Errorf("group %s still has policy %q attached", group, policy)
		}
	}
	return nil
}

// Migrated from main's read/delete/create error-path work.
func TestGroupPolicyAttachmentResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// RustFS answers 404 NoSuchResource for a group it does not have.
			name: "missing group is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "/group",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist"),
			}},
			wantRemoved: true,
		},
		{
			// The group exists but its policy list no longer names the
			// attached policy, so the attachment was detached out-of-band.
			name: "policy no longer on the group is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "/group",
				status: http.StatusOK, body: `{"name":"tf-test","status":"enabled","members":[],"policy":"other-policy"}`,
			}},
			wantRemoved: true,
		},
		{
			name: "still attached policy keeps state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "/group",
				status: http.StatusOK, body: `{"name":"tf-test","status":"enabled","members":[],"policy":"other-policy, tf-test-policy"}`,
			}},
		},
		{
			name: "server error keeps state and names the failing call",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "/group",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error reading group policy attachment",
			wantDetail:  "Could not read group tf-test: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupPolicyAttachmentResource(), client)

			resp := h.read(&GroupPolicyAttachmentResourceModel{
				Group:  types.StringValue("tf-test"),
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
			assertStateString(t, resp.State, path.Root("id"), "tf-test/tf-test-policy")
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// A group that is gone cannot still carry the policy.
			name: "already deleted group is not an error",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist"),
			}},
		},
		{
			name: "real server error is reported",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error detaching policy from group",
			wantDetail:  "Could not detach policy: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupPolicyAttachmentResource(), client)

			resp := h.delete(&GroupPolicyAttachmentResourceModel{
				Group:  types.StringValue("tf-test"),
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
		plan        *GroupPolicyAttachmentResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			name: "rejected attach reports a diagnostic and writes no state",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/attach",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist"),
			}},
			plan: &GroupPolicyAttachmentResourceModel{
				Group:  types.StringValue("tf-test"),
				Policy: types.StringValue("tf-test-policy"),
				ID:     types.StringNull(),
			},
			wantSummary: "Error attaching policy to group",
			wantDetail:  "Could not attach policy: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupPolicyAttachmentResource(), client)

			resp := h.create(tc.plan)

			rec.assertRequest(t, "policy/attach")
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewGroupPolicyAttachmentResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
