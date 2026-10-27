// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

// rustfs_bucket_metadata_backup_import has a single required attribute,
// content_base64, and it carries RequiresReplace(). Terraform can therefore
// never dispatch to Update: a changed archive is planned as a replacement and
// applied through Create. This file asserts that contract and follows the
// replacement through the server.

const (
	bucketMetadataBackupImportQuotaA = 1111
	bucketMetadataBackupImportQuotaB = 2222
	bucketMetadataBackupImportQuotaC = 3333
)

// TestAccBucketMetadataBackupImportResource_replaceAndErrors covers the
// replacement contract of rustfs_bucket_metadata_backup_import: create against
// a real archive, plan the archive change as a replacement, apply it so the
// create path really runs again, prove an out-of-band import cannot make the
// plan non-empty (Read only echoes state), and reject an invalid payload.
func TestAccBucketMetadataBackupImportResource_replaceAndErrors(t *testing.T) {
	bucket := fmt.Sprintf("tf-acc-bm-import-%d", acctest.RandInt())

	archiveA := testAccBucketMetadataBackupImportArchive(t, bucket, bucketMetadataBackupImportQuotaA)
	archiveB := testAccBucketMetadataBackupImportArchive(t, bucket, bucketMetadataBackupImportQuotaB)
	archiveC := testAccBucketMetadataBackupImportArchive(t, bucket, bucketMetadataBackupImportQuotaC)

	// The bucket itself is destroyed by its own resource; the imported quota
	// metadata is not (the import resource cannot delete what it imported), so
	// remove it here.
	t.Cleanup(func() {
		client := testAccRustClient()
		_ = client.DeletQuota(context.Background(), bucket)
	})

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketMetadataBackupImportConfig(bucket, archiveA),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bucketMetadataBackupImportResourceName, "content_base64", archiveA),
					testAccCheckBucketMetadataBackupImportQuota(t, bucket, bucketMetadataBackupImportQuotaA),
				),
			},
			{
				// RequiresReplace: the archive change must be planned as a
				// replacement, never as an in-place update.
				Config:             testAccBucketMetadataBackupImportConfig(bucket, archiveB),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(bucketMetadataBackupImportResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement: the create path runs again and the
				// server really holds the second archive's quota.
				Config: testAccBucketMetadataBackupImportConfig(bucket, archiveB),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bucketMetadataBackupImportResourceName, "content_base64", archiveB),
					testAccCheckBucketMetadataBackupImportQuota(t, bucket, bucketMetadataBackupImportQuotaB),
				),
			},
			{
				// Computed consistency: nothing left to do.
				Config:             testAccBucketMetadataBackupImportConfig(bucket, archiveB),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: a third archive is imported straight
				// through the admin API, behind Terraform's back. The server
				// moves to quota C while the state keeps archive B and the plan
				// stays empty, because Read only echoes the stored state.
				Config:             testAccBucketMetadataBackupImportConfig(bucket, archiveB),
				ExpectNonEmptyPlan: false,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckBucketMetadataBackupImportQuota(t, bucket, bucketMetadataBackupImportQuotaB),
					resource.TestCheckResourceAttr(bucketMetadataBackupImportResourceName, "content_base64", archiveB),
					testAccBucketMetadataBackupImportOutOfBand(t, archiveC, bucket, bucketMetadataBackupImportQuotaC),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Invalid base64 never reaches the server: the resource rejects
				// it with its own diagnostic.
				Config:      testAccBucketMetadataBackupImportConfig(bucket, "not-a-base64-archive!!"),
				ExpectError: regexp.MustCompile(`Invalid base64 data`),
			},
			{
				// A valid base64 payload that is not a ZIP archive is refused by
				// the server, and the resource surfaces that refusal.
				Config:      testAccBucketMetadataBackupImportConfig(bucket, base64.StdEncoding.EncodeToString([]byte("this is not a zip archive"))),
				ExpectError: regexp.MustCompile(`Error importing bucket metadata`),
			},
		},
	})
}

// TestBucketMetadataBackupImportResourceContentBase64RequiresReplace drives the
// attribute's real plan modifier: a changed archive must force a replacement, an
// unchanged one must not. That is what makes Update unreachable for this
// resource.
func TestBucketMetadataBackupImportResourceContentBase64RequiresReplace(t *testing.T) {
	ctx := context.Background()
	sch := resourceSchemaOf(t, NewBucketMetadataBackupImportResource())

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

func testAccBucketMetadataBackupImportConfig(bucket, contentBase64 string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_metadata_backup_import" "test" {
  content_base64 = "%s"

  # The metadata import targets an existing bucket, so it must run after it.
  depends_on = [rustfs_bucket.test]
}
`, bucket, contentBase64)
}

// testAccBucketMetadataBackupImportArchive builds a base64 ZIP in the layout
// RustFS exports for bucket metadata: one directory per bucket holding the
// metadata documents. It is the shape the equivalent data source produces.
func testAccBucketMetadataBackupImportArchive(t *testing.T, bucket string, quota int) string {
	t.Helper()

	document, err := json.Marshal(struct {
		Quota     int    `json:"quota"`
		QuotaType string `json:"quota_type"`
	}{Quota: quota, QuotaType: "Hard"})
	if err != nil {
		t.Fatalf("marshal quota document: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(bucket + "/quota.json")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write(document); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// testAccCheckBucketMetadataBackupImportQuota exports the bucket metadata from
// the server and checks the quota stored for the bucket, which proves the
// import ran and what it wrote.
func testAccCheckBucketMetadataBackupImportQuota(t *testing.T, bucket string, want int) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		got, err := testAccBucketMetadataBackupImportServerQuota(bucket)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("server quota for bucket %s = %d, want %d", bucket, got, want)
		}
		t.Logf("server reports quota %d for bucket %s", got, bucket)
		return nil
	}
}

// testAccBucketMetadataBackupImportOutOfBand imports a third archive directly
// through the admin API, without Terraform, and checks the server moved on.
func testAccBucketMetadataBackupImportOutOfBand(t *testing.T, archive, bucket string, wantQuota int) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		data, err := base64.StdEncoding.DecodeString(archive)
		if err != nil {
			return fmt.Errorf("decode out-of-band archive: %w", err)
		}
		client := testAccRustClient()
		if err := client.ImportBucketMetadata(context.Background(), data); err != nil {
			return fmt.Errorf("out-of-band bucket metadata import: %w", err)
		}

		got, err := testAccBucketMetadataBackupImportServerQuota(bucket)
		if err != nil {
			return err
		}
		if got != wantQuota {
			return fmt.Errorf("server quota for bucket %s after the out-of-band import = %d, want %d", bucket, got, wantQuota)
		}
		t.Logf("imported quota %d out of band for bucket %s; the follow-up plan must still be empty", got, bucket)
		return nil
	}
}

// testAccBucketMetadataBackupImportServerQuota reads the quota the server holds
// for the bucket by parsing its own metadata export, which is the document the
// import endpoint writes.
func testAccBucketMetadataBackupImportServerQuota(bucket string) (int, error) {
	client := testAccRustClient()
	exported, err := client.ExportBucketMetadata(context.Background())
	if err != nil {
		return 0, fmt.Errorf("export bucket metadata: %w", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(exported), int64(len(exported)))
	if err != nil {
		return 0, fmt.Errorf("read exported archive: %w", err)
	}

	entry := bucket + "/quota.json"
	for _, file := range reader.File {
		if file.Name != entry {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return 0, fmt.Errorf("open %s: %w", entry, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", entry, err)
		}

		var document struct {
			Quota *int `json:"quota"`
		}
		if err := json.Unmarshal(body, &document); err != nil {
			return 0, fmt.Errorf("parse %s (%s): %w", entry, body, err)
		}
		if document.Quota == nil {
			return 0, fmt.Errorf("%s carries no quota: %s", entry, body)
		}
		return *document.Quota, nil
	}

	return 0, fmt.Errorf("the server holds no %s after the import", entry)
}
