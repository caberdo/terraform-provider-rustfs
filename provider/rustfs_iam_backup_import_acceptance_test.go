// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// rustfs_iam_backup_import has a single required attribute, content_base64, and
// it carries RequiresReplace(). Terraform can therefore never dispatch to
// Update: a changed archive is planned as a replacement and applied through
// Create. This file asserts that contract, then follows the replacement through
// the server and covers the two input errors the resource has to reject.

// TestAccIamBackupImportResource_replaceAndErrors covers the replacement
// contract of rustfs_iam_backup_import: create against a real archive, plan the
// archive change as a replacement, apply it so the create path really runs
// again, prove an out-of-band change cannot make the plan non-empty (Read only
// echoes state), and reject an invalid payload before it reaches the server.
func TestAccIamBackupImportResource_replaceAndErrors(t *testing.T) {
	suffix := acctest.RandInt()
	policyA := fmt.Sprintf("tf-acc-iam-import-a-%d", suffix)
	policyB := fmt.Sprintf("tf-acc-iam-import-b-%d", suffix)

	archiveA := testAccIamBackupImportArchive(t, map[string]string{
		"iam-assets/policies.json": fmt.Sprintf(
			`{"%s":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::tf-acc-iam-import-a/*"]}]}}`,
			policyA),
	})
	archiveB := testAccIamBackupImportArchive(t, map[string]string{
		"iam-assets/policies.json": fmt.Sprintf(
			`{"%s":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObjectVersion"],"Resource":["arn:aws:s3:::tf-acc-iam-import-b/*"]}]}}`,
			policyB),
	})

	// The import resource cannot delete what it imported (its Delete is a
	// no-op), so the policies it creates are removed here. The drift step below
	// already deletes policyB, hence the errors are ignored.
	t.Cleanup(func() {
		client := testAccRustClient()
		for _, name := range []string{policyA, policyB} {
			_ = client.DeletePolicy(context.Background(), name)
		}
	})

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccIamBackupImportConfig(archiveA),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(iamBackupImportResourceName, "content_base64", archiveA),
					testAccCheckIamBackupImportPolicy(policyA, "s3:GetObject"),
				),
			},
			{
				// RequiresReplace: the archive change must be planned as a
				// replacement, never as an in-place update.
				Config:             testAccIamBackupImportConfig(archiveB),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(iamBackupImportResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement: the create path runs again and the
				// server really holds the second archive's policy.
				Config: testAccIamBackupImportConfig(archiveB),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(iamBackupImportResourceName, "content_base64", archiveB),
					testAccCheckIamBackupImportPolicy(policyB, "s3:GetObjectVersion"),
				),
			},
			{
				// Computed consistency: nothing left to do.
				Config:             testAccIamBackupImportConfig(archiveB),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: policyB disappears from the server without
				// Terraform knowing. The plan stays empty, because Read only
				// echoes the stored state, and the state keeps the archive the
				// last apply sent.
				Config:             testAccIamBackupImportConfig(archiveB),
				ExpectNonEmptyPlan: false,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckIamBackupImportPolicy(policyB, "s3:GetObjectVersion"),
					resource.TestCheckResourceAttr(iamBackupImportResourceName, "content_base64", archiveB),
					testAccIamBackupImportDeletePolicyOutOfBand(t, policyB),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Invalid base64 never reaches the server: the resource rejects
				// it with its own diagnostic.
				Config:      testAccIamBackupImportConfig("not-a-base64-archive!!"),
				ExpectError: regexp.MustCompile(`Invalid base64 data`),
			},
			{
				// A valid base64 payload that is not a ZIP archive is refused by
				// the server, and the resource surfaces that refusal.
				Config:      testAccIamBackupImportConfig(base64.StdEncoding.EncodeToString([]byte("this is not a zip archive"))),
				ExpectError: regexp.MustCompile(`Error importing IAM data`),
			},
		},
	})
}

// TestIamBackupImportResourceContentBase64RequiresReplace drives the attribute's
// real plan modifier: a changed archive must force a replacement, an unchanged
// one must not. That is what makes Update unreachable for this resource.
func TestIamBackupImportResourceContentBase64RequiresReplace(t *testing.T) {
	ctx := context.Background()
	sch := resourceSchemaOf(t, NewIamBackupImportResource())

	attr, ok := sch.Attributes["content_base64"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("content_base64 is not a StringAttribute: %T", sch.Attributes["content_base64"])
	}

	objectType, ok := sch.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("schema type is not an object: %T", sch.Type().TerraformType(ctx))
	}

	archiveA := base64.StdEncoding.EncodeToString([]byte("archive-a"))
	archiveB := base64.StdEncoding.EncodeToString([]byte("archive-b"))

	value := func(content string) tftypes.Value {
		return tftypes.NewValue(objectType, map[string]tftypes.Value{
			"content_base64": tftypes.NewValue(tftypes.String, content),
		})
	}

	for _, tc := range []struct {
		name            string
		state, plan     string
		wantReplacement bool
	}{
		{name: "changed", state: archiveA, plan: archiveB, wantReplacement: true},
		{name: "unchanged", state: archiveA, plan: archiveA, wantReplacement: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := planmodifier.StringRequest{
				Path:       path.Root("content_base64"),
				Config:     tfsdk.Config{Schema: sch, Raw: value(tc.plan)},
				Plan:       tfsdk.Plan{Schema: sch, Raw: value(tc.plan)},
				State:      tfsdk.State{Schema: sch, Raw: value(tc.state)},
				PlanValue:  types.StringValue(tc.plan),
				StateValue: types.StringValue(tc.state),
			}

			resp := planmodifier.StringResponse{}
			for _, modifier := range attr.PlanModifiers {
				modifier.PlanModifyString(ctx, req, &resp)
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("plan modifier diagnostics: %v", resp.Diagnostics)
			}
			if resp.RequiresReplace != tc.wantReplacement {
				t.Errorf("RequiresReplace = %v, want %v", resp.RequiresReplace, tc.wantReplacement)
			}
		})
	}
}

func testAccIamBackupImportConfig(contentBase64 string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_iam_backup_import" "test" {
  content_base64 = "%s"
}
`, contentBase64)
}

// testAccIamBackupImportArchive builds a base64 ZIP archive in the layout
// RustFS exports for IAM and returns it in the form the resource consumes.
func testAccIamBackupImportArchive(t *testing.T, entries map[string]string) string {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// testAccCheckIamBackupImportPolicy reads the policy back from the server and
// checks the action this archive asked for, which proves the import ran.
func testAccCheckIamBackupImportPolicy(name, action string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		policy, err := client.ReadPolicy(context.Background(), name)
		if err != nil {
			return fmt.Errorf("imported policy %s not found on the server: %w", name, err)
		}
		if policy.Name != name {
			return fmt.Errorf("imported policy name = %q, want %q", policy.Name, name)
		}
		for _, statement := range policy.Statement {
			for _, got := range statement.Action {
				if got == action {
					return nil
				}
			}
		}
		return fmt.Errorf("imported policy %s does not carry action %q: %+v", name, action, policy.Statement)
	}
}

// testAccIamBackupImportDeletePolicyOutOfBand removes an imported policy
// through the admin client, behind Terraform's back.
func testAccIamBackupImportDeletePolicyOutOfBand(t *testing.T, name string) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client := testAccRustClient()
		if err := client.DeletePolicy(context.Background(), name); err != nil {
			return fmt.Errorf("out-of-band delete of policy %s: %w", name, err)
		}
		if _, err := client.ReadPolicy(context.Background(), name); err == nil {
			return fmt.Errorf("policy %s still readable after the out-of-band delete", name)
		}
		t.Logf("deleted policy %s out of band; the follow-up plan must still be empty", name)
		return nil
	}
}
