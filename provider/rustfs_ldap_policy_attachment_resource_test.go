// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const (
	testAccLdapPolicyUserTargetDefault  = "uid=terraform-acc-test,ou=people,dc=example,dc=com"
	testAccLdapPolicyGroupTargetDefault = "cn=terraform-acc-test,ou=groups,dc=example,dc=com"
)

func testAccLdapPolicyUserTarget() string {
	if v := os.Getenv("RUSTFS_LDAP_POLICY_USER_TARGET"); v != "" {
		return v
	}
	return testAccLdapPolicyUserTargetDefault
}

func testAccLdapPolicyGroupTarget() string {
	if v := os.Getenv("RUSTFS_LDAP_POLICY_GROUP_TARGET"); v != "" {
		return v
	}
	return testAccLdapPolicyGroupTargetDefault
}

// testAccLdapPolicyPreCheck probes the server to confirm the LDAP policy
// endpoint is reachable and the target user_or_group exists. Without a
// configured LDAP identity provider (or without the target entity) the attach
// fails with a "not exist" error, in which case the test skips gracefully.
func testAccLdapPolicyPreCheck(t *testing.T, target string, isGroup bool) {
	testAccPreCheck(t)
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Ssl:          false,
	})
	req := rustfs.LDAPPolicyAttachment{
		UserOrGroup: target,
		PolicyName:  "readwrite",
		IsGroup:     isGroup,
	}
	if err := client.AttachLDAPPolicy(context.Background(), req); err != nil {
		if strings.Contains(err.Error(), "not exist") || strings.Contains(err.Error(), "does not exist") {
			t.Skipf("no LDAP identity provider configured (or target %q does not exist); "+
				"skipping LDAP policy attachment acceptance test. RustFS 1.0.x has no way to "+
				"configure an LDAP IdP, so this is the answer on every supported version: the "+
				"create/read/update/delete/import flows are covered by "+
				"TestLDAPPolicyAttachmentResource* against a mocked admin API instead. Point "+
				"RUSTFS_LDAP_POLICY_USER_TARGET/RUSTFS_LDAP_POLICY_GROUP_TARGET at an existing "+
				"user/group on a cluster that does have an LDAP IdP to run this test.", target)
		}
		t.Fatalf("unexpected error probing LDAP policy attachment: %v", err)
	}
	if err := client.DetachLDAPPolicy(context.Background(), req); err != nil {
		t.Fatalf("failed to clean up LDAP policy probe: %v", err)
	}
}

func TestAccLdapPolicyAttachment_basic(t *testing.T) {
	target := testAccLdapPolicyUserTarget()
	resourceName := "rustfs_ldap_policy_attachment.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLdapPolicyPreCheck(t, target, false) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccLdapPolicyAttachmentConfig("test", target, "readwrite", "false"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user_or_group", target),
					resource.TestCheckResourceAttr(resourceName, "policy", "readwrite"),
					resource.TestCheckResourceAttr(resourceName, "is_group", "false"),
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     target + "/readwrite",
			},
		},
	})
}

func TestAccLdapPolicyAttachment_group(t *testing.T) {
	target := testAccLdapPolicyGroupTarget()
	resourceName := "rustfs_ldap_policy_attachment.group"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLdapPolicyPreCheck(t, target, true) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccLdapPolicyAttachmentConfig("group", target, "readonly", "true"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user_or_group", target),
					resource.TestCheckResourceAttr(resourceName, "policy", "readonly"),
					resource.TestCheckResourceAttr(resourceName, "is_group", "true"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     target + "/readonly/true",
			},
		},
	})
}

func testAccLdapPolicyAttachmentConfig(resourceLabel, target, policy, isGroup string) string {
	return testAccProviderConfig() + `
resource "rustfs_ldap_policy_attachment" "` + resourceLabel + `" {
  user_or_group = "` + target + `"
  policy        = "` + policy + `"
  is_group      = ` + isGroup + `
}
`
}

// The acceptance tests above skip themselves: the attach endpoint answers "user
// not exist" unless RustFS knows the LDAP identity, and RustFS 1.0.x cannot be
// given an LDAP identity provider (see .github/CONTRIBUTING.md, "Acceptance
// tests"). The flows below drive Create, Read, Update, Delete and ImportState
// against the mocked admin API harness in provider_test.go, which also pins the
// wire contract: the target travels as the `user` or `group` field next to the
// `policies` list, and the identifier is `<user_or_group>/<policy>`.

const (
	ldapPolicyAttachPath = "/rustfs/admin/v3/idp/ldap/policy/attach"
	ldapPolicyDetachPath = "/rustfs/admin/v3/idp/ldap/policy/detach"
)

// ldapPolicyBody is the attach/detach payload.
type ldapPolicyBody struct {
	Policies []string `json:"policies"`
	User     string   `json:"user"`
	Group    string   `json:"group"`
}

func ldapPolicyPlan(target, policy string, isGroup types.Bool) LDAPPolicyAttachmentResourceModel {
	return LDAPPolicyAttachmentResourceModel{
		UserOrGroup: types.StringValue(target),
		Policy:      types.StringValue(policy),
		IsGroup:     isGroup,
		ID:          types.StringNull(),
	}
}

// ldapPolicyAttachBody decodes the body the resource sent to attach or detach.
func ldapPolicyAttachBody(t *testing.T, request adminRequest) ldapPolicyBody {
	t.Helper()
	var body ldapPolicyBody
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatalf("%s %s body is not JSON: %v", request.Method, request.Path, err)
	}
	return body
}

func TestLDAPPolicyAttachmentResourceCreate(t *testing.T) {
	ctx := context.Background()
	r := &LDAPPolicyAttachmentResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	plan := ldapPolicyPlan(ldapSAUserDN, "readwrite", types.BoolValue(false))
	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, LDAPPolicyAttachmentResourceModel{})}
	r.Create(ctx, frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, plan)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var created LDAPPolicyAttachmentResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &created))
	if got, want := created.ID.ValueString(), ldapSAUserDN+"/readwrite"; got != want {
		t.Errorf("create id = %q, want %q", got, want)
	}
	if created.UserOrGroup != plan.UserOrGroup || created.Policy != plan.Policy || !created.IsGroup.Equal(plan.IsGroup) {
		t.Errorf("create state target/policy/is_group = %q/%q/%v, want %q/%q/%v",
			created.UserOrGroup.ValueString(), created.Policy.ValueString(), created.IsGroup,
			plan.UserOrGroup.ValueString(), plan.Policy.ValueString(), plan.IsGroup)
	}

	request := recorder.only(t)
	if request.Method != http.MethodPost || request.Path != ldapPolicyAttachPath {
		t.Fatalf("create sent %s %s, want POST %s", request.Method, request.Path, ldapPolicyAttachPath)
	}
	attach := ldapPolicyAttachBody(t, request)
	if len(attach.Policies) != 1 || attach.Policies[0] != "readwrite" {
		t.Errorf("attach policies = %v, want [readwrite]", attach.Policies)
	}
	if attach.User != ldapSAUserDN || attach.Group != "" {
		t.Errorf("attach user/group = %q/%q, want the DN in the user field", attach.User, attach.Group)
	}
}

func TestLDAPPolicyAttachmentResourceCreateGroupTarget(t *testing.T) {
	ctx := context.Background()
	r := &LDAPPolicyAttachmentResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	const group = "cn=terraform-acc-test,ou=groups,dc=example,dc=com"
	plan := ldapPolicyPlan(group, "readonly", types.BoolValue(true))
	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, LDAPPolicyAttachmentResourceModel{})}
	r.Create(ctx, frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, plan)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	attach := ldapPolicyAttachBody(t, recorder.only(t))
	if attach.Group != group || attach.User != "" {
		t.Errorf("attach user/group = %q/%q, want the DN in the group field", attach.User, attach.Group)
	}
}

// An is_group left unset in the plan is not filled in by Create: the schema's
// default applies during plan, and the wire body must still be a user
// attachment.
func TestLDAPPolicyAttachmentResourceCreateDefaultsToUser(t *testing.T) {
	ctx := context.Background()
	r := &LDAPPolicyAttachmentResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	plan := ldapPolicyPlan(ldapSAUserDN, "readwrite", types.BoolNull())
	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, LDAPPolicyAttachmentResourceModel{})}
	r.Create(ctx, frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, plan)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	attach := ldapPolicyAttachBody(t, recorder.only(t))
	if attach.User != ldapSAUserDN || attach.Group != "" {
		t.Errorf("attach user/group = %q/%q, want the DN in the user field", attach.User, attach.Group)
	}
}

func TestLDAPPolicyAttachmentResourceCreateError(t *testing.T) {
	r := &LDAPPolicyAttachmentResource{}
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusBadRequest, `<Error><Code>InvalidArgument</Code><Message>user not exist</Message></Error>`)
	r.client = newUnitAllClient(srv)

	plan := ldapPolicyPlan(ldapSAUserDN, "readwrite", types.BoolValue(false))
	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, LDAPPolicyAttachmentResourceModel{})}
	r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, plan)}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error attaching policy to LDAP entity")
}

// Read has no per-target endpoint: it preserves the attachment, applies the
// documented is_group default and recomputes the identifier, without calling the
// server.
func TestLDAPPolicyAttachmentResourceRead(t *testing.T) {
	ctx := context.Background()
	r := &LDAPPolicyAttachmentResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	state := ldapPolicyPlan(ldapSAUserDN, "readwrite", types.BoolNull())
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, state)}
	r.Read(ctx, frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var read LDAPPolicyAttachmentResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &read))
	if read.IsGroup.IsNull() || read.IsGroup.ValueBool() {
		t.Errorf("read is_group = %v, want false", read.IsGroup)
	}
	if got, want := read.ID.ValueString(), ldapSAUserDN+"/readwrite"; got != want {
		t.Errorf("read id = %q, want %q", got, want)
	}
	if got := recorder.count(); got != 0 {
		t.Errorf("read called the admin API %d times, want none", got)
	}
}

// All attributes are RequiresReplace, but Update has to stay correct if
// Terraform ever calls it: it re-attaches and recomputes the identifier.
func TestLDAPPolicyAttachmentResourceUpdate(t *testing.T) {
	ctx := context.Background()
	r := &LDAPPolicyAttachmentResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	state := ldapPolicyPlan(ldapSAUserDN, "readwrite", types.BoolValue(false))
	state.ID = types.StringValue(ldapSAUserDN + "/readwrite")
	resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, state)}
	r.Update(ctx, frameworkresource.UpdateRequest{
		Plan:  resourcePlan(t, sch, state),
		State: resourceState(t, sch, state),
	}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var afterUpdate LDAPPolicyAttachmentResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &afterUpdate))
	if got := afterUpdate.ID.ValueString(); got != ldapSAUserDN+"/readwrite" {
		t.Errorf("update id = %q", got)
	}
	attach := ldapPolicyAttachBody(t, recorder.only(t))
	if attach.User != ldapSAUserDN || len(attach.Policies) != 1 || attach.Policies[0] != "readwrite" {
		t.Errorf("update body user/policies = %q/%v", attach.User, attach.Policies)
	}
}

func TestLDAPPolicyAttachmentResourceUpdateError(t *testing.T) {
	r := &LDAPPolicyAttachmentResource{}
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusBadRequest, `<Error><Code>InvalidArgument</Code><Message>user not exist</Message></Error>`)
	r.client = newUnitAllClient(srv)

	state := ldapPolicyPlan(ldapSAUserDN, "readwrite", types.BoolValue(false))
	resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, state)}
	r.Update(context.Background(), frameworkresource.UpdateRequest{
		Plan:  resourcePlan(t, sch, state),
		State: resourceState(t, sch, state),
	}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error attaching policy to LDAP entity")
}

func TestLDAPPolicyAttachmentResourceDelete(t *testing.T) {
	ctx := context.Background()
	r := &LDAPPolicyAttachmentResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	state := ldapPolicyPlan(ldapSAUserDN, "readwrite", types.BoolValue(false))
	resp := &frameworkresource.DeleteResponse{State: resourceState(t, sch, state)}
	r.Delete(ctx, frameworkresource.DeleteRequest{State: resourceState(t, sch, state)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	request := recorder.only(t)
	if request.Method != http.MethodPost || request.Path != ldapPolicyDetachPath {
		t.Fatalf("delete sent %s %s, want POST %s", request.Method, request.Path, ldapPolicyDetachPath)
	}
	detach := ldapPolicyAttachBody(t, request)
	if detach.User != ldapSAUserDN || len(detach.Policies) != 1 || detach.Policies[0] != "readwrite" {
		t.Errorf("detach body user/policies = %q/%v", detach.User, detach.Policies)
	}
}

func TestLDAPPolicyAttachmentResourceDeleteError(t *testing.T) {
	r := &LDAPPolicyAttachmentResource{}
	sch := resourceSchemaOf(t, r)
	// A real failure: `400 InvalidArgument "user not exist"` is no longer an
	// error here — an LDAP entity that is gone cannot still carry the policy,
	// so the detach is already satisfied (see TestLdapPolicyAttachmentResourceLifecyclePaths,
	// which pins that case alongside the unrelated-message one).
	srv, _ := newRecorderServer(t, http.StatusForbidden, `<Error><Code>AccessDenied</Code><Message>nope</Message></Error>`)
	r.client = newUnitAllClient(srv)

	state := ldapPolicyPlan(ldapSAUserDN, "readwrite", types.BoolValue(false))
	resp := &frameworkresource.DeleteResponse{State: resourceState(t, sch, state)}
	r.Delete(context.Background(), frameworkresource.DeleteRequest{State: resourceState(t, sch, state)}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error detaching policy from LDAP entity")
}

func TestLDAPPolicyAttachmentResourceImportState(t *testing.T) {
	ctx := context.Background()
	const dn = "uid=alice,ou=people,dc=example,dc=com"

	for _, tc := range []struct {
		name        string
		id          string
		wantTarget  string
		wantPolicy  string
		wantIsGroup types.Bool
		wantErr     string
	}{
		{
			name:        "user and policy",
			id:          dn + "/readwrite",
			wantTarget:  dn,
			wantPolicy:  "readwrite",
			wantIsGroup: types.BoolNull(),
		},
		{
			name:        "group and policy",
			id:          dn + "/readonly/true",
			wantTarget:  dn,
			wantPolicy:  "readonly",
			wantIsGroup: types.BoolValue(true),
		},
		{
			name:        "explicit user flag",
			id:          dn + "/readwrite/false",
			wantTarget:  dn,
			wantPolicy:  "readwrite",
			wantIsGroup: types.BoolValue(false),
		},
		{
			name:       "distinguished name containing a slash",
			id:         "cn=a/b,ou=people,dc=example,dc=com/readwrite",
			wantTarget: "cn=a/b,ou=people,dc=example,dc=com",
			wantPolicy: "readwrite",
		},
		{
			name:    "malformed",
			id:      "readwrite",
			wantErr: "Invalid import ID",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &LDAPPolicyAttachmentResource{}
			sch := resourceSchemaOf(t, r)
			resp := &frameworkresource.ImportStateResponse{State: resourceState(t, sch, LDAPPolicyAttachmentResourceModel{})}
			r.ImportState(ctx, frameworkresource.ImportStateRequest{ID: tc.id}, resp)
			if tc.wantErr != "" {
				assertDiagnosticSummary(t, resp.Diagnostics, tc.wantErr)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)

			var target, policy types.String
			requireNoDiagnostics(t, resp.State.GetAttribute(ctx, path.Root("user_or_group"), &target))
			requireNoDiagnostics(t, resp.State.GetAttribute(ctx, path.Root("policy"), &policy))
			if target.ValueString() != tc.wantTarget {
				t.Errorf("imported user_or_group = %q, want %q", target.ValueString(), tc.wantTarget)
			}
			if policy.ValueString() != tc.wantPolicy {
				t.Errorf("imported policy = %q, want %q", policy.ValueString(), tc.wantPolicy)
			}
			var isGroup types.Bool
			requireNoDiagnostics(t, resp.State.GetAttribute(ctx, path.Root("is_group"), &isGroup))
			if !isGroup.Equal(tc.wantIsGroup) {
				t.Errorf("imported is_group = %v, want %v", isGroup, tc.wantIsGroup)
			}
		})
	}
}

// TestLdapPolicyAttachmentResourceLifecyclePaths covers the paths the
// acceptance suite cannot reach with a live server: the idempotent delete of
// an LDAP entity that is already gone, and every failing API call in Create
// and Delete. The admin API exposes no per-target read-back for LDAP policy
// attachments, so Read cannot observe a missing entity and is covered for the
// state it does preserve.
func TestLdapPolicyAttachmentResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("read: preserves state because no read-back endpoint exists", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewLDAPPolicyAttachmentResource(), client)

		resp := h.read(&LDAPPolicyAttachmentResourceModel{
			UserOrGroup: types.StringValue("uid=tf-test,ou=people,dc=example,dc=com"),
			Policy:      types.StringValue("tf-test-policy"),
			IsGroup:     types.BoolValue(false),
			ID:          types.StringValue("uid=tf-test,ou=people,dc=example,dc=com/tf-test-policy"),
		})

		rec.assertNoRequest(t, "policy/")
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("id"), "uid=tf-test,ou=people,dc=example,dc=com/tf-test-policy")
	})

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		isGroup     bool
		wantSummary string
		wantDetail  string
	}{
		{
			// The endpoint answers 400 InvalidArgument "user not exist" for an
			// LDAP user it does not have (probed against RustFS latest).
			name: "already deleted LDAP user is not an error",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusBadRequest, body: s3ErrorXML("InvalidArgument", "user not exist"),
			}},
		},
		{
			// An LDAP group that is gone answers 404 NoSuchResource (probed
			// against RustFS latest).
			name: "already deleted LDAP group is not an error",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'cn=tf-test' does not exist"),
			}},
			isGroup: true,
		},
		{
			// Another InvalidArgument message is a real failure and must not
			// be swallowed as "already gone".
			name: "unrelated invalid argument is reported",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusBadRequest, body: s3ErrorXML("InvalidArgument", "invalid policy name"),
			}},
			wantSummary: "Error detaching policy from LDAP entity",
			wantDetail:  "Could not detach policy: ",
		},
		{
			name: "real server error is reported",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error detaching policy from LDAP entity",
			wantDetail:  "Could not detach policy: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewLDAPPolicyAttachmentResource(), client)

			resp := h.delete(&LDAPPolicyAttachmentResourceModel{
				UserOrGroup: types.StringValue("uid=tf-test,ou=people,dc=example,dc=com"),
				Policy:      types.StringValue("tf-test-policy"),
				IsGroup:     types.BoolValue(tc.isGroup),
				ID:          types.StringValue("uid=tf-test,ou=people,dc=example,dc=com/tf-test-policy"),
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
		plan        *LDAPPolicyAttachmentResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			name: "rejected attach reports a diagnostic and writes no state",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/attach",
				status: http.StatusBadRequest, body: s3ErrorXML("InvalidArgument", "user not exist"),
			}},
			plan: &LDAPPolicyAttachmentResourceModel{
				UserOrGroup: types.StringValue("uid=tf-test,ou=people,dc=example,dc=com"),
				Policy:      types.StringValue("tf-test-policy"),
				IsGroup:     types.BoolValue(false),
				ID:          types.StringNull(),
			},
			wantSummary: "Error attaching policy to LDAP entity",
			wantDetail:  "Could not attach policy: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewLDAPPolicyAttachmentResource(), client)

			resp := h.create(tc.plan)

			rec.assertRequest(t, "policy/attach")
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewLDAPPolicyAttachmentResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
