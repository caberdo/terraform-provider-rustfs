// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestGroupResourceSchema(t *testing.T) {
	r := NewGroupResource()
	resp := &frameworkresource.SchemaResponse{}
	r.Schema(context.TODO(), frameworkresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	if _, ok := attrs["name"]; !ok {
		t.Error("expected name attribute")
	}
	if _, ok := attrs["status"]; !ok {
		t.Error("expected status attribute")
	}
	if _, ok := attrs["members"]; !ok {
		t.Error("expected members attribute")
	}
}

func TestGroupResourceMetadata(t *testing.T) {
	r := NewGroupResource()
	resp := &frameworkresource.MetadataResponse{}
	r.Metadata(context.TODO(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_group" {
		t.Errorf("expected rustfs_group, got %s", resp.TypeName)
	}
}

// TestAccGroupResource_basic walks the whole lifecycle of a group: an in-place
// change of members and status that is asserted against the server, the revert,
// computed consistency, the replacement planned by the name's RequiresReplace,
// import by the plain group name, and a group removed behind Terraform's back.
func TestAccGroupResource_basic(t *testing.T) {
	groupName := fmt.Sprintf("tf-test-group-%d", acctest.RandInt())
	renamedGroup := groupName + "-renamed"
	userA := fmt.Sprintf("tf-test-group-user-a-%d", acctest.RandInt())
	userB := fmt.Sprintf("tf-test-group-user-b-%d", acctest.RandInt())
	resourceName := "rustfs_group.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckGroupDestroy,
		Steps: []resource.TestStep{
			// Create the group with one of the two member users.
			{
				Config: testAccGroupConfig(groupName, userA, userB, "enabled", false),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", groupName),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
					resource.TestCheckResourceAttr(resourceName, "members.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "members.*", userA),
					testAccCheckGroupState(groupName, "enabled", userA),
				),
			},
			// In-place update: a second member and a disabled status, both read
			// back from the server.
			{
				Config: testAccGroupConfig(groupName, userA, userB, "disabled", true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "status", "disabled"),
					resource.TestCheckResourceAttr(resourceName, "members.#", "2"),
					resource.TestCheckTypeSetElemAttr(resourceName, "members.*", userA),
					resource.TestCheckTypeSetElemAttr(resourceName, "members.*", userB),
					testAccCheckGroupState(groupName, "disabled", userA, userB),
				),
			},
			// Shrinking the group must keep the surviving member and the status.
			{
				Config: testAccGroupConfig(groupName, userA, userB, "disabled", false),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "status", "disabled"),
					resource.TestCheckResourceAttr(resourceName, "members.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "members.*", userA),
					testAccCheckGroupState(groupName, "disabled", userA),
				),
			},
			// Revert both attributes.
			{
				Config: testAccGroupConfig(groupName, userA, userB, "enabled", true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
					resource.TestCheckResourceAttr(resourceName, "members.#", "2"),
					testAccCheckGroupState(groupName, "enabled", userA, userB),
				),
			},
			// The configuration leaves "members" to the provider: the group must
			// keep the membership it already has while only the status changes.
			{
				Config: testAccGroupConfigWithoutMembers(groupName, userA, userB, "disabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "status", "disabled"),
					resource.TestCheckResourceAttr(resourceName, "members.#", "2"),
					resource.TestCheckTypeSetElemAttr(resourceName, "members.*", userA),
					resource.TestCheckTypeSetElemAttr(resourceName, "members.*", userB),
					testAccCheckGroupState(groupName, "disabled", userA, userB),
				),
			},
			// Computed consistency for that same configuration: the post-apply
			// plan must be empty rather than an endless no-op update.
			{
				Config:             testAccGroupConfigWithoutMembers(groupName, userA, userB, "disabled"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Revert both attributes once the membership is spelled out again.
			{
				Config: testAccGroupConfig(groupName, userA, userB, "enabled", true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
					resource.TestCheckResourceAttr(resourceName, "members.#", "2"),
					testAccCheckGroupState(groupName, "enabled", userA, userB),
				),
			},
			// Computed consistency: the post-apply plan must be empty.
			{
				Config:             testAccGroupConfig(groupName, userA, userB, "enabled", true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// "name" is the only RequiresReplace attribute, so renaming the
			// group must plan a replacement.
			{
				Config:             testAccGroupConfig(renamedGroup, userA, userB, "enabled", true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace)},
				},
			},
			// Apply the replacement so the replacement path really runs: the old
			// group has members, which must not block its destruction.
			{
				Config: testAccGroupConfig(renamedGroup, userA, userB, "enabled", true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", renamedGroup),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
					resource.TestCheckResourceAttr(resourceName, "members.#", "2"),
					testAccCheckGroupState(renamedGroup, "enabled", userA, userB),
				),
			},
			// Import by the plain group name.
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        renamedGroup,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
			// Drift: the group is removed outside Terraform, so the plan the
			// framework computes after this step must not be empty. The check
			// runs before that plan, which is why the mutation is expected to
			// show up there.
			{
				Config:             testAccGroupConfig(renamedGroup, userA, userB, "enabled", true),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupExists(resourceName),
					testAccDeleteGroupOutOfBand(renamedGroup),
				),
			},
			// Converge back to the configured group.
			{
				Config: testAccGroupConfig(renamedGroup, userA, userB, "enabled", true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckGroupExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", renamedGroup),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
					resource.TestCheckResourceAttr(resourceName, "members.#", "2"),
					testAccCheckGroupState(renamedGroup, "enabled", userA, userB),
				),
			},
		},
	})
}

// testAccGroupUsersConfig renders the two member users shared by every group
// configuration.
func testAccGroupUsersConfig(accessKeyA, accessKeyB string) string {
	return fmt.Sprintf(`
resource "rustfs_user" "a" {
  access_key = "%s"
  secret_key = "tf-test-group-secret-a-123!"
  policy     = ""
}

resource "rustfs_user" "b" {
  access_key = "%s"
  secret_key = "tf-test-group-secret-b-123!"
  policy     = ""
}
`, accessKeyA, accessKeyB)
}

// testAccGroupConfig renders the group with an explicit member list. withB
// toggles the second user's membership so the same test can grow and shrink the
// group without replacing the users.
func testAccGroupConfig(groupName, accessKeyA, accessKeyB, status string, withB bool) string {
	members := "rustfs_user.a.access_key"
	if withB {
		members += ", rustfs_user.b.access_key"
	}

	return testAccProviderConfig() + testAccGroupUsersConfig(accessKeyA, accessKeyB) + fmt.Sprintf(`
resource "rustfs_group" "test" {
  name    = "%s"
  status  = "%s"
  members = [%s]
}
`, groupName, status, members)
}

// testAccGroupConfigWithoutMembers leaves the member list to the provider, so
// the group must keep the membership it already has.
func testAccGroupConfigWithoutMembers(groupName, accessKeyA, accessKeyB, status string) string {
	return testAccProviderConfig() + testAccGroupUsersConfig(accessKeyA, accessKeyB) + fmt.Sprintf(`
resource "rustfs_group" "test" {
  name   = "%s"
  status = "%s"
}
`, groupName, status)
}

// testAccCheckGroupExists requires the group named in state to exist on the
// server.
func testAccCheckGroupExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}

		name := rs.Primary.Attributes["name"]
		if name == "" {
			return fmt.Errorf("no name set for %s", n)
		}

		client := testAccRustClient()
		info, err := client.GetGroup(context.Background(), name)
		if err != nil {
			return fmt.Errorf("group %s not found: %w", name, err)
		}
		if info.Name != name {
			return fmt.Errorf("expected group %s, got %s", name, info.Name)
		}
		return nil
	}
}

// testAccCheckGroupState requires the server to report exactly the given
// members and status: a missing member means an update was dropped, an extra
// one means a member was left behind.
func testAccCheckGroupState(group, status string, members ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		info, err := client.GetGroup(context.Background(), group)
		if err != nil {
			return fmt.Errorf("failed to read group %s: %w", group, err)
		}
		if info.Status != status {
			return fmt.Errorf("group %s has status %q, expected %q", group, info.Status, status)
		}

		got := slices.Clone(info.Members)
		slices.Sort(got)
		want := slices.Clone(members)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			return fmt.Errorf("group %s has members %v, expected %v", group, got, want)
		}
		return nil
	}
}

// testAccDeleteGroupOutOfBand removes the group without Terraform, the way an
// operator would with the CLI. The admin API refuses to delete a group that
// still has members, so its members are detached first.
func testAccDeleteGroupOutOfBand(group string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		info, err := client.GetGroup(context.Background(), group)
		if err != nil {
			return fmt.Errorf("failed to read group %s before deleting it outside Terraform: %w", group, err)
		}
		if len(info.Members) > 0 {
			if err := client.UpdateGroupMembers(context.Background(), rustfs.GroupAddRemove{
				Group:    group,
				Members:  info.Members,
				IsRemove: true,
			}); err != nil {
				return fmt.Errorf("failed to detach the members of %s outside Terraform: %w", group, err)
			}
		}
		if err := client.DeleteGroup(context.Background(), group); err != nil {
			return fmt.Errorf("failed to delete group %s outside Terraform: %w", group, err)
		}
		return nil
	}
}

// Destruction is verified against the server so a group left behind by a
// failed delete is reported instead of silently ignored.
func testAccCheckGroupDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_group" {
			continue
		}
		name := rs.Primary.Attributes["name"]
		if name == "" {
			continue
		}
		if _, err := client.GetGroup(context.Background(), name); err != nil {
			if rustfs.IsAPIErrorCode(err, "NoSuchResource", "NoSuchGroup") {
				continue
			}
			return fmt.Errorf("failed to read group %s: %w", name, err)
		}
		return fmt.Errorf("group %s still exists", name)
	}
	return nil
}

// Migrated from main's read/delete/create error-path work.
func TestGroupResourceLifecyclePaths(t *testing.T) {
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
			name:        "missing group is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading group",
			wantDetail:  "Could not read group: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupResource(), client)

			resp := h.read(&GroupResourceModel{Name: types.StringValue("tf-test"), Members: nullStringSet()})

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
			name:   "already deleted group is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting group",
			wantDetail:  "Could not delete group: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupResource(), client)

			resp := h.delete(&GroupResourceModel{Name: types.StringValue("tf-test"), Members: nullStringSet()})

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
		window      *GroupResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			// A member that does not exist is rejected by update-group-members.
			name:        "rejected members report a diagnostic and write no state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "user 'ghost' does not exist")}},
			window:      &GroupResourceModel{Name: types.StringValue("tf-test"), Members: stringSetFromSlice([]string{"ghost"})},
			wantSummary: "Error creating group",
			wantDetail:  "user 'ghost' does not exist",
		},
		{
			name: "failed status change is reported",
			routes: []requestRoute{
				{method: http.MethodPut, pathContains: "update-group-members", status: http.StatusOK},
				{method: http.MethodPut, pathContains: "set-group-status", status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "status exploded")},
			},
			window:      &GroupResourceModel{Name: types.StringValue("tf-test"), Status: types.StringValue("disabled"), Members: nullStringSet()},
			wantSummary: "Error setting group status",
			wantDetail:  "status exploded",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupResource(), client)

			resp := h.create(tc.window)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.window)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewGroupResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
