// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const bucketMetadataBackupImportResourceName = "rustfs_bucket_metadata_backup_import.test"

// bucketMetadataBackupImportPayload is the decoded archive the tests feed the
// resource: the ZIP local-file magic plus a body, so the assertion compares
// real bytes rather than a round-tripped string.
const bucketMetadataBackupImportPayload = "PK\x03\x04archive"

func TestBucketMetadataBackupImportResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewBucketMetadataBackupImportResource())

	attrs := sch.GetAttributes()
	if len(attrs) != 1 {
		t.Errorf("expected 1 attribute, got %d: %v", len(attrs), attrs)
	}

	content, ok := attrs["content_base64"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("content_base64 is not a StringAttribute")
	}
	if !content.Required || content.Optional || content.Computed {
		t.Errorf("content_base64 must be Required only: %+v", content)
	}
	if !content.Sensitive {
		t.Error("content_base64 carries the whole bucket metadata backup and must be Sensitive")
	}
	if !hasRequiresReplace(content.PlanModifiers) {
		t.Error("content_base64 must force recreation, an import is a one-shot action")
	}
	if content.Description == "" {
		t.Error("content_base64 has no description, tfplugindocs would publish an empty row")
	}
}

func TestBucketMetadataBackupImportResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewBucketMetadataBackupImportResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_metadata_backup_import" {
		t.Errorf("expected rustfs_bucket_metadata_backup_import, got %s", resp.TypeName)
	}
}

func TestBucketMetadataBackupImportResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected a diagnostic for unexpected provider data")
		}
		if got := resp.Diagnostics[0].Summary(); got != "Unexpected Resource Configure Type" {
			t.Errorf("unexpected diagnostic summary: %s", got)
		}
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestBucketMetadataBackupImportResourceCreate(t *testing.T) {
	t.Run("imports the archive", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		model := BucketMetadataBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload))),
		}
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, model)}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodPut || request.Path != "/rustfs/admin/v3/import-bucket-metadata" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if string(request.Body) != bucketMetadataBackupImportPayload {
			t.Errorf("the decoded archive was not sent: %q", request.Body)
		}

		var got BucketMetadataBackupImportResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ContentBase64.ValueString() != model.ContentBase64.ValueString() {
			t.Errorf("state was not written from the plan: %+v", got)
		}
	})

	t.Run("rejects invalid base64 before calling the API", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, BucketMetadataBackupImportResourceModel{
			ContentBase64: types.StringValue("not base64!"),
		})
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: plan}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid base64 data")
		if got := recorder.count(); got != 0 {
			t.Errorf("expected no admin API request, got %d", got)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, "<Error><Code>MetadataImportFailed</Code><Message>import failed</Message></Error>")
		r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, BucketMetadataBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload))),
		})
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: plan}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error importing bucket metadata")
		if !strings.Contains(diagnostic.Detail(), "MetadataImportFailed") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

func TestBucketMetadataBackupImportResourceUpdate(t *testing.T) {
	t.Run("imports the archive", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := BucketMetadataBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload))),
		}
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  resourcePlan(t, sch, plan),
			State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{}),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodPut || request.Path != "/rustfs/admin/v3/import-bucket-metadata" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if string(request.Body) != bucketMetadataBackupImportPayload {
			t.Errorf("the decoded archive was not sent: %q", request.Body)
		}

		var got BucketMetadataBackupImportResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ContentBase64.ValueString() != plan.ContentBase64.ValueString() {
			t.Errorf("state was not written from the plan: %+v", got)
		}
	})

	t.Run("rejects invalid base64 before calling the API", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, BucketMetadataBackupImportResourceModel{
			ContentBase64: types.StringValue("not base64!"),
		})
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  plan,
			State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{}),
		}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid base64 data")
		if got := recorder.count(); got != 0 {
			t.Errorf("expected no admin API request, got %d", got)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, "<Error><Code>MetadataImportFailed</Code></Error>")
		r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, BucketMetadataBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload))),
		})
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  plan,
			State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{}),
		}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error importing bucket metadata")
		if !strings.Contains(diagnostic.Detail(), "MetadataImportFailed") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

func TestBucketMetadataBackupImportResourceRead(t *testing.T) {
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
	r.client = newUnitAllClient(srv)
	sch := resourceSchemaOf(t, r)

	state := BucketMetadataBackupImportResourceModel{
		ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload))),
	}
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{})}
	r.Read(context.Background(), frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got BucketMetadataBackupImportResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ContentBase64.ValueString() != state.ContentBase64.ValueString() {
		t.Errorf("Read dropped state: %+v", got)
	}
	if got := recorder.count(); got != 0 {
		t.Errorf("Read must not call the admin API, saw %d requests", got)
	}
}

func TestBucketMetadataBackupImportResourceDelete(t *testing.T) {
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r := NewBucketMetadataBackupImportResource().(*BucketMetadataBackupImportResource)
	r.client = newUnitAllClient(srv)
	sch := resourceSchemaOf(t, r)

	resp := &frameworkresource.DeleteResponse{}
	r.Delete(context.Background(), frameworkresource.DeleteRequest{
		State: resourceState(t, sch, BucketMetadataBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload))),
		}),
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if got := recorder.count(); got != 0 {
		t.Errorf("Delete is a no-op and must not call the admin API, saw %d requests", got)
	}
}

// Bucket metadata import acceptance coverage. The import route is registered on
// RustFS 1.0.x: importing is a write the server performs immediately, so the
// resource has no readable state beyond the archive it was given and no
// ImportState.

func TestAccBucketMetadataBackupImportResource(t *testing.T) {
	archive := testAccBucketMetadataBackupArchive(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket_metadata_backup_import" "test" {
  content_base64 = %q
}
`, archive),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(bucketMetadataBackupImportResourceName, "content_base64"),
					resource.TestCheckResourceAttr(bucketMetadataBackupImportResourceName, "content_base64", archive),
				),
			},
		},
	})
}

// testAccBucketMetadataBackupArchive exports the live bucket metadata once so
// the configuration carries a stable archive. The export is currently
// byte-stable, but a data source would still re-export on every plan and
// content_base64 forces replacement, so the step would depend on the cluster
// not changing mid-plan.
func testAccBucketMetadataBackupArchive(t *testing.T) string {
	t.Helper()
	testAccPreCheck(t)
	client := testAccRustClient()
	data, err := client.ExportBucketMetadata(context.Background())
	if err != nil {
		t.Fatalf("exporting the live bucket metadata: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// TestBucketMetadataBackupImportResourceLifecyclePaths covers the paths the
// acceptance suite cannot reach with a live server: an archive the server
// rejects, an archive that is not base64 at all, and the client-side read/delete
// steps that must not touch the server.
func TestBucketMetadataBackupImportResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name   string
		routes []requestRoute
	}{
		{name: "read keeps the imported archive and ignores a failing server", routes: []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}}},
		{name: "read keeps the imported archive when the server is healthy", routes: []requestRoute{{status: http.StatusOK, body: `{"ok":true}`}}},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			// Read only mirrors state, so no response, failing or not, may be
			// used and no request may be sent.
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketMetadataBackupImportResource(), client)

			resp := h.read(&BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))})

			requireNoDiagnostics(t, resp.Diagnostics)
			assertStatePresent(t, resp.State)
			assertStateString(t, resp.State, path.Root("content_base64"), base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))
			rec.assertNoRequest(t, "import-bucket-metadata")
		})
	}

	deleteCases := []struct {
		name   string
		routes []requestRoute
	}{
		{name: "delete is a no-op with a failing server", routes: []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}}},
		{name: "delete is a no-op with a healthy server", routes: []requestRoute{{status: http.StatusOK, body: `{"ok":true}`}}},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketMetadataBackupImportResource(), client)

			resp := h.delete(&BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))})

			requireNoDiagnostics(t, resp.Diagnostics)
			rec.assertNoRequest(t, "import-bucket-metadata")
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		model       *BucketMetadataBackupImportResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "accepted archive writes state",
			routes: []requestRoute{{method: http.MethodPut, pathContains: "import-bucket-metadata", status: http.StatusOK}},
			model:  &BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))},
		},
		{
			name:        "invalid base64 is rejected before any request",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")}},
			model:       &BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue("not base64!!")},
			wantSummary: "Invalid base64 data",
			wantDetail:  "illegal base64 data",
		},
		{
			name:        "rejected archive reports the server error and writes no state",
			routes:      []requestRoute{{method: http.MethodPut, pathContains: "import-bucket-metadata", status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "invalid zip archive")}},
			model:       &BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))},
			wantSummary: "Error importing bucket metadata",
			wantDetail:  "Could not import: ",
		},
		{
			name:        "forbidden import names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			model:       &BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))},
			wantSummary: "Error importing bucket metadata",
			wantDetail:  "Could not import: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketMetadataBackupImportResource(), client)

			resp := h.create(tc.model)

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("content_base64"), base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))
				rec.assertRequest(t, "import-bucket-metadata")
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.model)
		})
	}

	updateCases := []struct {
		name        string
		routes      []requestRoute
		model       *BucketMetadataBackupImportResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "accepted archive writes state",
			routes: []requestRoute{{method: http.MethodPut, pathContains: "import-bucket-metadata", status: http.StatusOK}},
			model:  &BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))},
		},
		{
			name:        "invalid base64 is rejected before any request",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")}},
			model:       &BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue("not base64!!")},
			wantSummary: "Invalid base64 data",
			wantDetail:  "illegal base64 data",
		},
		{
			name:        "rejected archive reports the server error",
			routes:      []requestRoute{{method: http.MethodPut, pathContains: "import-bucket-metadata", status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "invalid zip archive")}},
			model:       &BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))},
			wantSummary: "Error importing bucket metadata",
			wantDetail:  "Could not import: ",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketMetadataBackupImportResource(), client)

			resp := h.update(tc.model, &BucketMetadataBackupImportResourceModel{ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(bucketMetadataBackupImportPayload)))})

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketMetadataBackupImportResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
