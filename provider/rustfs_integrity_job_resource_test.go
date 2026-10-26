// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

const integrityJobTestID = "0b6e6d1a-4a6b-4a5a-9a37-2f1a3c4d5e6f"

const integrityJobTestPausedPayload = `{
  "format_version": 1,
  "id": "` + integrityJobTestID + `",
  "bucket": "logs",
  "bucket_incarnation": "7f1d2c3b-4a59-4687-9a01-2b3c4d5e6f70",
  "request": {
    "mode": "audit",
    "items": [{"key": "reports/2026.csv", "version_id": null, "expected_sha256": null, "target_key": null}],
    "bytes_per_second": 8388608,
    "max_object_bytes": 5368709120
  },
  "state": "paused",
  "revision": 0,
  "results": [{"state": "pending", "source_fingerprint": null, "expected_sha256": null,
    "evidence_source": null, "source_size": null, "target_version_id": null, "detail": null}],
  "last_error": null
}`

const integrityJobTestRunningPayload = `{
  "format_version": 1,
  "id": "` + integrityJobTestID + `",
  "bucket": "logs",
  "bucket_incarnation": "7f1d2c3b-4a59-4687-9a01-2b3c4d5e6f70",
  "request": {
    "mode": "audit",
    "items": [{"key": "reports/2026.csv", "version_id": null, "expected_sha256": null, "target_key": null}],
    "bytes_per_second": 8388608,
    "max_object_bytes": 5368709120
  },
  "state": "running",
  "revision": 1,
  "results": [{"state": "pending", "source_fingerprint": null, "expected_sha256": null,
    "evidence_source": null, "source_size": null, "target_version_id": null, "detail": null}],
  "last_error": null
}`

const integrityJobTestReadinessPayload = `{
  "local_reader_supported": true,
  "write_requested": false,
  "fleet_operator_attested": false,
  "new_writes_enabled_here": false,
  "fleet_capability_verified": false,
  "unverified_requirements": ["peer_reader_writer_and_repair_capabilities"]
}`

func integrityJobTestItems(t *testing.T) types.List {
	t.Helper()
	items, diags := types.ListValueFrom(context.Background(),
		types.ObjectType{AttrTypes: integrityJobItemAttrTypes},
		[]IntegrityJobItemModel{{
			Key:            types.StringValue("reports/2026.csv"),
			VersionID:      types.StringNull(),
			ExpectedSHA256: types.StringNull(),
			TargetKey:      types.StringNull(),
		}})
	if diags.HasError() {
		t.Fatalf("building items: %v", diags)
	}
	return items
}

func integrityJobTestPlan(t *testing.T) IntegrityJobResourceModel {
	t.Helper()
	return IntegrityJobResourceModel{
		ID:                types.StringNull(),
		Bucket:            types.StringValue("logs"),
		Mode:              types.StringValue("audit"),
		Items:             integrityJobTestItems(t),
		BytesPerSecond:    types.Int64Value(8388608),
		MaxObjectBytes:    types.Int64Value(5368709120),
		Confirm:           types.StringValue("logs"),
		State:             types.StringNull(),
		Revision:          types.Int64Null(),
		BucketIncarnation: types.StringNull(),
		Results:           types.ListNull(types.ObjectType{AttrTypes: integrityJobResultAttrTypes}),
		LastError:         types.StringNull(),
	}
}

func TestIntegrityJobResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewIntegrityJobResource())

	required := []string{"bucket", "mode", "items", "confirm"}
	for _, name := range required {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsRequired() {
			t.Errorf("attribute %q must be Required", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}

	computed := []string{"id", "state", "revision", "bucket_incarnation", "results", "last_error"}
	for _, name := range computed {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsComputed() || attr.IsRequired() {
			t.Errorf("attribute %q must be Computed", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}

	for _, name := range []string{"bucket", "mode", "confirm"} {
		attr := sch.Attributes[name].(resourceschema.StringAttribute)
		if !hasRequiresReplace(attr.PlanModifiers) {
			t.Errorf("attribute %q must carry RequiresReplace", name)
		}
	}

	bps := sch.Attributes["bytes_per_second"].(resourceschema.Int64Attribute)
	if bps.Default == nil {
		t.Error("bytes_per_second must carry a default")
	}
	mob := sch.Attributes["max_object_bytes"].(resourceschema.Int64Attribute)
	if mob.Default == nil {
		t.Error("max_object_bytes must carry a default")
	}
}

func TestIntegrityJobResourceMetadata(t *testing.T) {
	resp := &resource.MetadataResponse{}
	NewIntegrityJobResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_integrity_job" {
		t.Errorf("expected rustfs_integrity_job, got %s", resp.TypeName)
	}
}

func TestIntegrityJobResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewIntegrityJobResource().(*IntegrityJobResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewIntegrityJobResource().(*IntegrityJobResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Resource Configure Type")
	})
}

func TestIntegrityJobResourceValidateConfig(t *testing.T) {
	client, _ := routedClient(t)
	h := newCRUDHarness(t, NewIntegrityJobResource(), client)

	t.Run("confirm must name the bucket", func(t *testing.T) {
		plan := integrityJobTestPlan(t)
		plan.Confirm = types.StringValue("other")
		resp := h.validateConfig(plan)
		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid integrity job configuration")
		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid integrity job configuration", "confirm")
	})

	t.Run("audit items must not carry target_key", func(t *testing.T) {
		plan := integrityJobTestPlan(t)
		plan.Items = integrityJobItemsWithTarget(t)
		resp := h.validateConfig(plan)
		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid integrity job configuration")
		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid integrity job configuration", "target_key")
	})

	t.Run("migrate items need target_key", func(t *testing.T) {
		plan := integrityJobTestPlan(t)
		plan.Mode = types.StringValue("migrate")
		resp := h.validateConfig(plan)
		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid integrity job configuration")
		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid integrity job configuration", "target_key")
	})

	t.Run("valid migrate", func(t *testing.T) {
		plan := integrityJobTestPlan(t)
		plan.Mode = types.StringValue("migrate")
		plan.Items = integrityJobItemsWithTarget(t)
		resp := h.validateConfig(plan)
		requireNoDiagnostics(t, resp.Diagnostics)
	})
}

func integrityJobItemsWithTarget(t *testing.T) types.List {
	t.Helper()
	items, diags := types.ListValueFrom(context.Background(),
		types.ObjectType{AttrTypes: integrityJobItemAttrTypes},
		[]IntegrityJobItemModel{{
			Key:            types.StringValue("reports/2026.csv"),
			VersionID:      types.StringNull(),
			ExpectedSHA256: types.StringNull(),
			TargetKey:      types.StringValue("reports/migrated/2026.csv"),
		}})
	if diags.HasError() {
		t.Fatalf("building items: %v", diags)
	}
	return items
}

func TestIntegrityJobResourceCreate(t *testing.T) {
	t.Run("creates, resumes and records the running job", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "/integrity/logs/jobs/" + integrityJobTestID + "/control", status: http.StatusOK, body: integrityJobTestRunningPayload},
			requestRoute{method: http.MethodPost, pathContains: "/integrity/logs/jobs", status: http.StatusCreated, body: integrityJobTestPausedPayload},
		)
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.create(integrityJobTestPlan(t))
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), integrityJobTestID)
		assertStateString(t, resp.State, path.Root("state"), "running")
		rec.assertRequest(t, "POST /rustfs/admin/v3/integrity/logs/jobs")
		rec.assertRequest(t, "POST /rustfs/admin/v3/integrity/logs/jobs/"+integrityJobTestID+"/control")

		var got IntegrityJobResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading state: %v", diags)
		}
		if got.Revision.ValueInt64() != 1 {
			t.Errorf("revision = %d", got.Revision.ValueInt64())
		}
		if got.Results.IsNull() || len(got.Results.Elements()) != 1 {
			t.Errorf("results = %v", got.Results)
		}
		if !got.LastError.IsNull() {
			t.Errorf("last_error = %v", got.LastError)
		}
	})

	t.Run("migrate is refused before a job is created when writes are not activated", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "/integrity/readiness", status: http.StatusOK, body: integrityJobTestReadinessPayload},
		)
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		plan := integrityJobTestPlan(t)
		plan.Mode = types.StringValue("migrate")
		plan.Items = integrityJobItemsWithTarget(t)

		resp := h.create(plan)
		assertErrorDiagnostic(t, resp.Diagnostics, "Data-integrity writes are not activated", "new_writes_enabled_here")
		h.assertNoPartialState(resp, plan)
		rec.assertNoRequest(t, "/jobs")
	})

	t.Run("a refused resume cancels the paused job and reports the reason", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "/control", status: http.StatusTooManyRequests, body: s3ErrorXML("SlowDown", "an integrity worker is already active; retry resume later")},
			requestRoute{method: http.MethodPost, pathContains: "/integrity/logs/jobs", status: http.StatusCreated, body: integrityJobTestPausedPayload},
		)
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.create(integrityJobTestPlan(t))
		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting integrity job", "an integrity worker is already active")
		h.assertNoPartialState(resp, integrityJobTestPlan(t))

		controlCalls := 0
		for _, request := range rec.requests {
			if strings.Contains(request, "/control") {
				controlCalls++
			}
		}
		if controlCalls != 2 {
			t.Errorf("control calls = %d, want resume plus cancel: %v", controlCalls, rec.requests)
		}
	})

	t.Run("explains a capability gap with the server version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "/integrity/logs/jobs", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"1.0.3"}`},
		)
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.create(integrityJobTestPlan(t))
		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Data-integrity jobs are not available on this RustFS server")
		for _, want := range []string{"RustFS 1.0.3", "POST /rustfs/admin/v3/integrity/{bucket}/jobs", "Upgrade RustFS"} {
			if !strings.Contains(diagnostic.Detail(), want) {
				t.Errorf("detail %q does not contain %q", diagnostic.Detail(), want)
			}
		}
	})
}

func TestIntegrityJobResourceRead(t *testing.T) {
	t.Run("mirrors the job state", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "/integrity/logs/jobs/" + integrityJobTestID, status: http.StatusOK, body: integrityJobTestRunningPayload},
		)
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.read(integrityJobImportedModel())
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("state"), "running")
		assertStateString(t, resp.State, path.Root("mode"), "audit")
		assertStateString(t, resp.State, path.Root("confirm"), "logs")
		rec.assertRequest(t, "GET /rustfs/admin/v3/integrity/logs/jobs/"+integrityJobTestID)

		var got IntegrityJobResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading state: %v", diags)
		}
		if got.Items.IsNull() || len(got.Items.Elements()) != 1 {
			t.Errorf("imported items were not restored: %v", got.Items)
		}
		if got.BytesPerSecond.ValueInt64() != 8388608 || got.MaxObjectBytes.ValueInt64() != 5368709120 {
			t.Errorf("limits = %d/%d", got.BytesPerSecond.ValueInt64(), got.MaxObjectBytes.ValueInt64())
		}
	})

	t.Run("removes a job the server no longer holds", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			status: http.StatusNotFound, body: s3ErrorXML("NoSuchKey", "integrity job not found"),
		})
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		state := integrityJobTestPlan(t)
		state.ID = types.StringValue(integrityJobTestID)
		resp := h.read(state)
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("removes a job whose bucket generation changed", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			status: http.StatusPreconditionFailed, body: s3ErrorXML("PreconditionFailed", "integrity job or target changed"),
		})
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.read(integrityJobImportedModel())
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("keeps state on an unexpected error", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom"),
		})
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		state := integrityJobTestPlan(t)
		state.ID = types.StringValue(integrityJobTestID)
		resp := h.read(state)
		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading integrity job", "boom")
		assertStatePresent(t, resp.State)
	})
}

func TestIntegrityJobResourceDelete(t *testing.T) {
	t.Run("cancels through the control route", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "/control", status: http.StatusOK, body: integrityJobTestPausedPayload},
		)
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		state := integrityJobTestPlan(t)
		state.ID = types.StringValue(integrityJobTestID)
		resp := h.delete(state)
		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "POST /rustfs/admin/v3/integrity/logs/jobs/"+integrityJobTestID+"/control")
	})

	t.Run("a missing job is already gone", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			status: http.StatusNotFound, body: s3ErrorXML("NoSuchKey", "integrity job not found"),
		})
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		state := integrityJobTestPlan(t)
		state.ID = types.StringValue(integrityJobTestID)
		resp := h.delete(state)
		requireNoDiagnostics(t, resp.Diagnostics)
	})

	t.Run("a stale bucket generation is already gone", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "/control", status: http.StatusPreconditionFailed, body: s3ErrorXML("PreconditionFailed", "integrity job or target changed")},
			requestRoute{method: http.MethodGet, pathContains: "/integrity/logs/jobs/" + integrityJobTestID, status: http.StatusPreconditionFailed, body: s3ErrorXML("PreconditionFailed", "integrity job or target changed")},
		)
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.delete(integrityJobImportedModel())
		requireNoDiagnostics(t, resp.Diagnostics)
	})

	t.Run("retries cancel once after a concurrent change", func(t *testing.T) {
		cancelCalls := 0
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			switch r.Method {
			case http.MethodGet:
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, integrityJobTestPausedPayload)
			case http.MethodPost:
				cancelCalls++
				if cancelCalls == 1 {
					w.WriteHeader(http.StatusPreconditionFailed)
					_, _ = fmt.Fprint(w, s3ErrorXML("PreconditionFailed", "integrity job or target changed"))
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, integrityJobTestPausedPayload)
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
		}))
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.delete(integrityJobImportedModel())
		requireNoDiagnostics(t, resp.Diagnostics)
		if cancelCalls != 2 {
			t.Errorf("cancel calls = %d, want 2", cancelCalls)
		}
	})

	t.Run("surfaces an unexpected cancel error", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom"),
		})
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		state := integrityJobTestPlan(t)
		state.ID = types.StringValue(integrityJobTestID)
		resp := h.delete(state)
		assertErrorDiagnostic(t, resp.Diagnostics, "Error cancelling integrity job", "boom")
	})
}

func integrityJobEmptyModel() IntegrityJobResourceModel {
	return IntegrityJobResourceModel{
		Items:   types.ListNull(types.ObjectType{AttrTypes: integrityJobItemAttrTypes}),
		Results: types.ListNull(types.ObjectType{AttrTypes: integrityJobResultAttrTypes}),
	}
}

func integrityJobImportedModel() IntegrityJobResourceModel {
	model := integrityJobEmptyModel()
	model.Bucket = types.StringValue("logs")
	model.ID = types.StringValue(integrityJobTestID)
	return model
}

func TestIntegrityJobResourceImportState(t *testing.T) {
	r := NewIntegrityJobResource()
	resp := &resource.ImportStateResponse{State: resourceState(t, resourceSchemaOf(t, r), integrityJobEmptyModel())}
	r.(resource.ResourceWithImportState).ImportState(
		context.Background(),
		resource.ImportStateRequest{ID: "logs/" + integrityJobTestID},
		resp,
	)
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("bucket"), "logs")
	assertStateString(t, resp.State, path.Root("id"), integrityJobTestID)
}

func TestIntegrityJobResourceImportStateRejectsMalformedID(t *testing.T) {
	r := NewIntegrityJobResource()
	resp := &resource.ImportStateResponse{State: resourceState(t, resourceSchemaOf(t, r), integrityJobEmptyModel())}
	r.(resource.ResourceWithImportState).ImportState(
		context.Background(),
		resource.ImportStateRequest{ID: "no-separator"},
		resp,
	)
	assertErrorDiagnostic(t, resp.Diagnostics, "Invalid import ID", "<bucket>/<job_id>")
}

// TestIntegrityJobResourceLifecyclePaths pins the cross-method contract in one
// place: a create records the job it started and writes nothing when any step
// fails, a job the server no longer holds is removed from state, a missing
// route is a capability gap rather than a vanished job, and destroy both
// cancels and stays idempotent for a stale record.
func TestIntegrityJobResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("configure rejects unexpected provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewIntegrityJobResource(), cannedClient(t, http.StatusOK, ""))
		resp := h.configure("not-a-client")
		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient")
	})

	createCases := []struct {
		name        string
		plan        func(t *testing.T) IntegrityJobResourceModel
		routes      []requestRoute
		wantSummary string
		wantDetail  []string
		wantNoJobs  bool
		wantControl int
	}{
		{
			name: "a migrate job without activated writes is refused before creating",
			plan: func(t *testing.T) IntegrityJobResourceModel {
				plan := integrityJobTestPlan(t)
				plan.Mode = types.StringValue("migrate")
				plan.Items = integrityJobItemsWithTarget(t)
				return plan
			},
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/integrity/readiness", status: http.StatusOK, body: integrityJobTestReadinessPayload},
			},
			wantSummary: "Data-integrity writes are not activated",
			wantDetail:  []string{"new_writes_enabled_here", "RUSTFS_SHARD_INTEGRITY_WRITE"},
			wantNoJobs:  true,
		},
		{
			name: "a missing route names the route and the server version",
			plan: integrityJobTestPlan,
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"1.0.3"}`},
				{method: http.MethodPost, pathContains: "/integrity/logs/jobs", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			},
			wantSummary: "Data-integrity jobs are not available on this RustFS server",
			wantDetail:  []string{"RustFS 1.0.3", "POST /rustfs/admin/v3/integrity/{bucket}/jobs", "Upgrade RustFS"},
		},
		{
			name: "a job without an id is reported instead of tracked",
			plan: integrityJobTestPlan,
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "/integrity/logs/jobs", status: http.StatusCreated, body: `{"id":"","state":"paused"}`},
			},
			wantSummary: "Error creating integrity job",
			wantDetail:  []string{"without an id"},
		},
		{
			name: "a refused resume cancels the paused job and reports why",
			plan: integrityJobTestPlan,
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "/control", status: http.StatusTooManyRequests,
					body: s3ErrorXML("SlowDown", "an integrity worker is already active; retry resume later")},
				{method: http.MethodPost, pathContains: "/integrity/logs/jobs", status: http.StatusCreated, body: integrityJobTestPausedPayload},
			},
			wantSummary: "Error starting integrity job",
			wantDetail:  []string{"an integrity worker is already active", "refused to resume it"},
			wantControl: 2,
		},
		{
			name: "a rejected create carries the server detail",
			plan: integrityJobTestPlan,
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "/integrity/logs/jobs", status: http.StatusBadRequest,
					body: s3ErrorXML("InvalidArgument", "a job requires 1..=64 explicit source versions")},
			},
			wantSummary: "Error creating integrity job",
			wantDetail:  []string{"explicit source versions"},
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, recorder := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewIntegrityJobResource(), client)
			plan := tc.plan(t)

			resp := h.create(plan)

			if len(resp.Diagnostics) != 1 {
				t.Fatalf("expected exactly one diagnostic, got %d: %v", len(resp.Diagnostics), resp.Diagnostics)
			}
			assertDiagnosticDetail(t, resp.Diagnostics, tc.wantDetail[0])
			for _, want := range tc.wantDetail[1:] {
				assertDiagnosticDetail(t, resp.Diagnostics, want)
			}
			if got := resp.Diagnostics[0].Summary(); got != tc.wantSummary {
				t.Errorf("diagnostic summary = %q, want %q", got, tc.wantSummary)
			}
			h.assertNoPartialState(resp, plan)
			if tc.wantNoJobs {
				recorder.assertNoRequest(t, "/jobs")
			}
			if tc.wantControl > 0 {
				controlCalls := 0
				for _, request := range recorder.snapshot() {
					if strings.Contains(request, "/control") {
						controlCalls++
					}
				}
				if controlCalls != tc.wantControl {
					t.Errorf("control calls = %d, want %d: %v", controlCalls, tc.wantControl, recorder.snapshot())
				}
			}
		})
	}

	t.Run("create: resumes the paused job and records it", func(t *testing.T) {
		client, recorder := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "/control", status: http.StatusOK, body: integrityJobTestRunningPayload},
			requestRoute{method: http.MethodPost, pathContains: "/integrity/logs/jobs", status: http.StatusCreated, body: integrityJobTestPausedPayload},
		)
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.create(integrityJobTestPlan(t))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), integrityJobTestID)
		assertStateString(t, resp.State, path.Root("state"), "running")
		assertStateString(t, resp.State, path.Root("bucket_incarnation"), "7f1d2c3b-4a59-4687-9a01-2b3c4d5e6f70")
		recorder.assertRequest(t, "POST /rustfs/admin/v3/integrity/logs/jobs")
		recorder.assertRequest(t, "POST /rustfs/admin/v3/integrity/logs/jobs/"+integrityJobTestID+"/control")
	})

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  []string
	}{
		{
			name: "an existing job is mirrored into state",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/integrity/logs/jobs/" + integrityJobTestID, status: http.StatusOK, body: integrityJobTestRunningPayload},
			},
		},
		{
			name: "a job the server no longer holds is removed from state",
			routes: []requestRoute{
				{status: http.StatusNotFound, body: s3ErrorXML("NoSuchKey", "integrity job not found")},
			},
			wantRemoved: true,
		},
		{
			name: "a changed bucket generation is removed from state",
			routes: []requestRoute{
				{status: http.StatusPreconditionFailed, body: s3ErrorXML("PreconditionFailed", "integrity job or target changed")},
			},
			wantRemoved: true,
		},
		{
			// A 404 whose body names no job code means the route was never
			// registered; dropping the record would silently forget a job the
			// server may still hold after an upgrade.
			name: "a missing route keeps the record",
			routes: []requestRoute{
				{status: http.StatusNotFound, body: "Not Found"},
			},
			wantSummary: "Error reading integrity job",
			wantDetail:  []string{"Could not read job " + integrityJobTestID, "Not Found"},
		},
		{
			name: "an unexpected failure keeps the record",
			routes: []requestRoute{
				{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")},
			},
			wantSummary: "Error reading integrity job",
			wantDetail:  []string{"Could not read job " + integrityJobTestID, "boom"},
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, recorder := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewIntegrityJobResource(), client)

			resp := h.read(integrityJobImportedModel())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("state"), "running")
				recorder.assertRequest(t, "GET /rustfs/admin/v3/integrity/logs/jobs/"+integrityJobTestID)
				return
			}
			if len(resp.Diagnostics) != 1 {
				t.Fatalf("expected exactly one diagnostic, got %d: %v", len(resp.Diagnostics), resp.Diagnostics)
			}
			assertDiagnosticDetail(t, resp.Diagnostics, tc.wantDetail[0])
			for _, want := range tc.wantDetail[1:] {
				assertDiagnosticDetail(t, resp.Diagnostics, want)
			}
			if got := resp.Diagnostics[0].Summary(); got != tc.wantSummary {
				t.Errorf("diagnostic summary = %q, want %q", got, tc.wantSummary)
			}
			assertStatePresent(t, resp.State)
		})
	}

	t.Run("update copies the plan without calling the server", func(t *testing.T) {
		client, recorder := routedClient(t)
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.update(integrityJobTestPlan(t), integrityJobTestPlan(t))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		if len(recorder.snapshot()) != 0 {
			t.Fatalf("update issued requests: %v", recorder.snapshot())
		}
	})

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  []string
	}{
		{
			name: "cancel goes through the control route",
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "/control", status: http.StatusOK, body: integrityJobTestPausedPayload},
			},
		},
		{
			name: "a missing job needs no cancel",
			routes: []requestRoute{
				{status: http.StatusNotFound, body: s3ErrorXML("NoSuchKey", "integrity job not found")},
			},
		},
		{
			name: "a stale generation needs no cancel",
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "/control", status: http.StatusPreconditionFailed, body: s3ErrorXML("PreconditionFailed", "integrity job or target changed")},
				{method: http.MethodGet, pathContains: "/integrity/logs/jobs/" + integrityJobTestID, status: http.StatusPreconditionFailed, body: s3ErrorXML("PreconditionFailed", "integrity job or target changed")},
			},
		},
		{
			name: "an unexpected cancel failure is surfaced",
			routes: []requestRoute{
				{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")},
			},
			wantSummary: "Error cancelling integrity job",
			wantDetail:  []string{"Could not cancel job " + integrityJobTestID, "boom"},
		},
	}

	for _, tc := range deleteCases {
		t.Run("destroy: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewIntegrityJobResource(), client)

			resp := h.delete(integrityJobImportedModel())

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			if len(resp.Diagnostics) != 1 {
				t.Fatalf("expected exactly one diagnostic, got %d: %v", len(resp.Diagnostics), resp.Diagnostics)
			}
			assertDiagnosticDetail(t, resp.Diagnostics, tc.wantDetail[0])
			for _, want := range tc.wantDetail[1:] {
				assertDiagnosticDetail(t, resp.Diagnostics, want)
			}
			if got := resp.Diagnostics[0].Summary(); got != tc.wantSummary {
				t.Errorf("diagnostic summary = %q, want %q", got, tc.wantSummary)
			}
		})
	}
}

// testAccIntegritySkipUnlessSupported skips when the running server does not
// implement the integrity routes, so the suite stays green on older releases.
func testAccIntegritySkipUnlessSupported(t *testing.T) {
	t.Helper()
	client := testAccRustClient()
	if _, err := client.IntegrityReadiness(); err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("data-integrity routes are not implemented on this server: %v", err)
		}
		t.Fatalf("probing data-integrity readiness: %v", err)
	}
}

// testAccIntegrityObject creates a bucket and one object out of band, because
// the provider has no object resource. The bucket and object are removed when
// the test finishes; the integrity job record stays on the server (there is no
// delete route) but the apply's destroy cancels it.
func testAccIntegrityObject(t *testing.T, bucket, key string) {
	t.Helper()
	client, err := testAccS3Client()
	if err != nil {
		t.Fatalf("building the S3 client: %v", err)
	}
	ctx := context.Background()
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("creating bucket %s: %v", bucket, err)
	}
	t.Cleanup(func() {
		_, _ = client.DeleteObject(context.Background(), &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
		_, _ = client.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	})
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader("integrity acceptance payload"),
	}); err != nil {
		t.Fatalf("putting %s/%s: %v", bucket, key, err)
	}
}

func TestAccIntegrityJobResource_audit(t *testing.T) {
	bucket := fmt.Sprintf("tf-integrity-%d", acctest.RandInt())
	key := "reports/2026.csv"
	resourceName := "rustfs_integrity_job.test"

	tfresource.Test(t, tfresource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testAccIntegritySkipUnlessSupported(t)
			testAccIntegrityObject(t, bucket, key)
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: testAccIntegrityJobConfig(bucket, key),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttrSet(resourceName, "id"),
					tfresource.TestCheckResourceAttr(resourceName, "bucket", bucket),
					tfresource.TestCheckResourceAttr(resourceName, "mode", "audit"),
					tfresource.TestCheckResourceAttr(resourceName, "confirm", bucket),
					tfresource.TestCheckResourceAttrSet(resourceName, "state"),
					tfresource.TestCheckResourceAttrSet(resourceName, "bucket_incarnation"),
				),
			},
			{
				// The job and its computed fields are server-owned, so a refresh
				// must not produce a plan for the unchanged configuration.
				Config:   testAccIntegrityJobConfig(bucket, key),
				PlanOnly: true,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func testAccIntegrityJobConfig(bucket, key string) string {
	return testAccProviderConfig() + `
resource "rustfs_integrity_job" "test" {
  bucket  = "` + bucket + `"
  mode    = "audit"
  confirm = "` + bucket + `"

  items = [{
    key = "` + key + `"
  }]
}
`
}

func TestIntegrityJobResourceUpdateCopiesPlan(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewIntegrityJobResource(), client)

	plan := integrityJobTestPlan(t)
	resp := h.update(plan, integrityJobTestPlan(t))
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)
	if len(rec.requests) != 0 {
		t.Fatalf("update issued requests: %v", rec.requests)
	}
}

func TestIntegrityJobResourceCreateErrors(t *testing.T) {
	t.Run("readiness read fails before a migrate job is created", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "/integrity/readiness",
			status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom"),
		})
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		plan := integrityJobTestPlan(t)
		plan.Mode = types.StringValue("migrate")
		plan.Items = integrityJobItemsWithTarget(t)

		resp := h.create(plan)
		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading data-integrity readiness", "boom")
		h.assertNoPartialState(resp, plan)
		rec.assertNoRequest(t, "/jobs")
	})

	t.Run("a job without an id is reported instead of tracked", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "/integrity/logs/jobs",
			status: http.StatusCreated, body: `{"id":"","state":"paused"}`,
		})
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.create(integrityJobTestPlan(t))
		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating integrity job", "without an id")
		h.assertNoPartialState(resp, integrityJobTestPlan(t))
	})

	t.Run("a rejected create carries the server detail", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "/integrity/logs/jobs",
			status: http.StatusBadRequest, body: s3ErrorXML("InvalidArgument", "a job requires 1..=64 explicit source versions"),
		})
		h := newCRUDHarness(t, NewIntegrityJobResource(), client)

		resp := h.create(integrityJobTestPlan(t))
		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating integrity job", "explicit source versions")
	})
}

func TestIntegrityJobResourceReadAndDeleteWithoutID(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewIntegrityJobResource(), client)

	readResp := h.read(integrityJobEmptyModel())
	requireNoDiagnostics(t, readResp.Diagnostics)
	assertStateRemoved(t, readResp.State)

	deleteResp := h.delete(integrityJobEmptyModel())
	requireNoDiagnostics(t, deleteResp.Diagnostics)

	if len(rec.requests) != 0 {
		t.Fatalf("empty id issued requests: %v", rec.requests)
	}
}
