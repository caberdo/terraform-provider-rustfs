// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestKmsBackupResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewKmsBackupResource())

	for _, want := range []string{"id", "backup_id", "snapshot_generation", "deployment_identity", "artifact_count", "manifest_digest", "backup_kek_id", "backup_kek_version", "sanitized_config_included", "export_supported", "unavailable_reason"} {
		if _, ok := sch.Attributes[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
	backupID, ok := sch.Attributes["backup_id"].(schema.StringAttribute)
	if !ok || !backupID.Required {
		t.Fatalf("backup_id must be Required: %#v", sch.Attributes["backup_id"])
	}
	if !hasRequiresReplace(backupID.PlanModifiers) {
		t.Error("backup_id must force replacement")
	}
}

func kmsBackupLifecycleModel() *kmsBackupResourceModel {
	return &kmsBackupResourceModel{
		ID:                      types.StringValue("nightly-1"),
		BackupID:                types.StringValue("nightly-1"),
		SnapshotGeneration:      types.Int64Value(42),
		DeploymentIdentity:      types.StringValue("deploy-1"),
		ArtifactCount:           types.Int64Value(3),
		ManifestDigest:          types.StringValue("abc123"),
		BackupKEKID:             types.StringValue("kek-1"),
		BackupKEKVersion:        types.Int64Value(3),
		SanitizedConfigIncluded: types.BoolValue(true),
		ExportSupported:         types.BoolValue(true),
		UnavailableReason:       types.StringNull(),
	}
}

func kmsBackupStatusBody(exportSupported bool, reason string) string {
	return `{"service_status":"healthy","backend":"local","responsibility":"full-material","export_supported":` + boolString(exportSupported) + `,"deployment_identity":"deploy-1","backup_root":"/backups","backup_kek_configured":true,"backup_kek_id":"kek-1","backup_kek_version":3,"target_key_dir":"/keys","unavailable_reason":` + reason + `}`
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestKmsBackupResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("create records the bundle metadata", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "kms/backup", status: http.StatusOK, body: kmsBackupStatusBody(true, "null")},
			requestRoute{method: http.MethodPost, pathContains: "kms/backup", status: http.StatusOK, body: `{"backup_id":"nightly-1","snapshot_generation":42,"deployment_identity":"deploy-1","artifact_count":3,"manifest_digest":"abc123","backup_kek_id":"kek-1","backup_kek_version":3,"sanitized_config_included":true}`},
		)
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		plan := kmsBackupLifecycleModel()
		plan.ID = types.StringNull()
		plan.SnapshotGeneration = types.Int64Null()
		plan.DeploymentIdentity = types.StringNull()
		plan.ArtifactCount = types.Int64Null()
		plan.ManifestDigest = types.StringNull()
		plan.BackupKEKID = types.StringNull()
		plan.BackupKEKVersion = types.Int64Null()
		plan.SanitizedConfigIncluded = types.BoolNull()
		plan.ExportSupported = types.BoolNull()
		plan.UnavailableReason = types.StringNull()

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/backup")
		assertStateString(t, resp.State, path.Root("backup_id"), "nightly-1")
		assertStateString(t, resp.State, path.Root("manifest_digest"), "abc123")
	})

	t.Run("create refuses to write when the server reports export as disabled", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/backup", status: http.StatusOK,
			body: kmsBackupStatusBody(false, `"RUSTFS_KMS_BACKUP_DIR is not set; KMS backup and restore are disabled until it is"`),
		})
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.create(kmsBackupLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "KMS backup export is disabled", "no bundle was written")
		for _, want := range []string{"RUSTFS_KMS_BACKUP_DIR", "RUSTFS_KMS_BACKUP_KEK_ID", "restart RustFS"} {
			assertDiagnosticDetail(t, resp.Diagnostics, want)
		}
		rec.assertNoRequest(t, "POST /rustfs/admin/v3/kms/backup")
	})

	t.Run("create reports a status read failure before writing", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/backup", status: http.StatusInternalServerError,
			body: s3ErrorXML("InternalError", "kms service is not running"),
		})
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.create(kmsBackupLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating KMS backup",
			"Could not check whether this server can export a KMS backup bundle")
		rec.assertNoRequest(t, "POST /rustfs/admin/v3/kms/backup")
	})

	t.Run("create reports an unsupported backend", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "kms/backup", status: http.StatusOK, body: kmsBackupStatusBody(true, "null")},
			requestRoute{method: http.MethodPost, pathContains: "kms/backup", status: http.StatusNotImplemented,
				body: `{"message":"backend aws cannot export a RustFS bundle"}`},
		)
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.create(kmsBackupLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating KMS backup", "does not support KMS backup")
	})

	t.Run("read keeps the bundle when the subsystem becomes unavailable", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/backup", status: http.StatusOK,
			body: kmsBackupStatusBody(false, `"RUSTFS_KMS_BACKUP_DIR is not set"`),
		})
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.read(kmsBackupLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("manifest_digest"), "abc123")
		assertStateString(t, resp.State, path.Root("unavailable_reason"), "RUSTFS_KMS_BACKUP_DIR is not set")
	})

	t.Run("read reports a status failure", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/backup", status: http.StatusInternalServerError,
			body: s3ErrorXML("InternalError", "kms service is not running"),
		})
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.read(kmsBackupLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading KMS backup status", "Could not read KMS backup status: ")
	})

	t.Run("delete makes no request", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.delete(kmsBackupLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/backup")
	})
}

// testAccKmsBackupSkipUnlessExport asserts the deployment's export readiness
// and skips with the server's own reason when it does not match. The stock
// acceptance deployment has no RUSTFS_KMS_BACKUP_DIR, so it reports
// export_supported=false; a backup-capable deployment reports true.
func testAccKmsBackupSkipUnlessExport(t *testing.T, want bool) {
	t.Helper()

	client := testAccRustClient()
	status, err := client.GetKmsBackupStatus(context.Background())
	if err != nil {
		t.Skipf("this deployment does not serve the KMS backup status route: %v", err)
	}
	if status.ExportSupported != want {
		reason := "the server reports no reason"
		if status.UnavailableReason != nil && *status.UnavailableReason != "" {
			reason = *status.UnavailableReason
		}
		backend := "unknown"
		if status.Backend != nil && *status.Backend != "" {
			backend = *status.Backend
		}
		t.Skipf("KMS backup export_supported=%t (backend %s, reason: %s); this case needs export_supported=%t",
			status.ExportSupported, backend, reason, want)
	}
}

// TestAccKmsBackupResource_exportDisabled asserts that Create refuses to write
// when the server reports export_supported=false and explains which server
// environment variables enable the subsystem. The stock acceptance deployment
// has no RUSTFS_KMS_BACKUP_DIR, so this case runs on every cell.
func TestAccKmsBackupResource_exportDisabled(t *testing.T) {
	testAccPreCheck(t)
	testAccKmsBackupSkipUnlessExport(t, false)

	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: testAccKmsBackupConfig("tf-acc-export-disabled"),
				ExpectError: regexp.MustCompile(
					`(?s)KMS backup export is disabled.*` +
						`RUSTFS_KMS_BACKUP_DIR.*` +
						`RUSTFS_KMS_BACKUP_KEK_ID`,
				),
			},
		},
	})
}

// TestAccKmsBackupResource_create writes a real bundle. It is opt-in: set the
// server's backup root (RUSTFS_KMS_BACKUP_DIR, RUSTFS_KMS_BACKUP_KEK and
// RUSTFS_KMS_BACKUP_KEK_ID) so the server reports export_supported=true. The
// bundle name is random unless RUSTFS_ACC_KMS_BACKUP_ID names it, because a
// destination that already holds a bundle refuses to mix in a second one.
func TestAccKmsBackupResource_create(t *testing.T) {
	testAccPreCheck(t)
	testAccKmsBackupSkipUnlessExport(t, true)

	backupID := os.Getenv("RUSTFS_ACC_KMS_BACKUP_ID")
	if backupID == "" {
		backupID = fmt.Sprintf("tf-acc-%d", acctest.RandInt())
	}
	resourceName := "rustfs_kms_backup.test"

	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckKmsBackupDestroyLeavesBundle,
		Steps: []tfresource.TestStep{
			{
				Config: testAccKmsBackupConfig(backupID),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "backup_id", backupID),
					tfresource.TestCheckResourceAttrPair(resourceName, "id", resourceName, "backup_id"),
					tfresource.TestCheckResourceAttrSet(resourceName, "snapshot_generation"),
					tfresource.TestCheckResourceAttrSet(resourceName, "manifest_digest"),
					tfresource.TestCheckResourceAttrSet(resourceName, "artifact_count"),
					tfresource.TestCheckResourceAttrSet(resourceName, "backup_kek_id"),
					tfresource.TestCheckResourceAttr(resourceName, "export_supported", "true"),
				),
			},
			{
				// The bundle is write-once server state; a refresh only reports
				// subsystem readiness, so the unchanged configuration plans
				// nothing.
				Config:   testAccKmsBackupConfig(backupID),
				PlanOnly: true,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func testAccKmsBackupConfig(backupID string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_kms_backup" "test" {
  backup_id = %q
}
`, backupID)
}

// testAccCheckKmsBackupDestroyLeavesBundle asserts the subsystem still reports
// export readiness after destroy. The server exposes no route that reads,
// verifies or deletes an individual bundle, so the bundle itself cannot be
// addressed from a test; what destroy promises is that it only forgets the
// record and leaves the backup subsystem (and therefore the bundle on disk)
// untouched.
func testAccCheckKmsBackupDestroyLeavesBundle(_ *terraform.State) error {
	client := testAccRustClient()
	status, err := client.GetKmsBackupStatus(context.Background())
	if err != nil {
		return fmt.Errorf("reading KMS backup status after destroy: %w", err)
	}
	if !status.ExportSupported {
		return fmt.Errorf("destroy left the KMS backup subsystem unable to export; the bundle may be gone")
	}
	return nil
}
