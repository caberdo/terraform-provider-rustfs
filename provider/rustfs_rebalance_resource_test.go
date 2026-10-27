// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const rebalanceResourceName = "rustfs_rebalance.test"

const (
	testRebalanceTriggerID      = "6f1f0b6a-9c1d-4f0e-8a2b-3d5c7e9f1a2b"
	testOtherRebalanceTriggerID = "11111111-2222-3333-4444-555555555555"
)

// rebalanceStatusJSON renders the report shape GET /rustfs/admin/v3/rebalance/status
// returns. progress is present only while a pool is moving data.
const rebalanceStatusJSON = `{
	"id":"` + testRebalanceTriggerID + `",
	"pools":[
		{
			"id":0,
			"status":"Started",
			"stopping":false,
			"used":0.42,
			"lastError":"slow disk",
			"cleanupWarnings":{
				"count":1,
				"lastMsg":"could not clean source",
				"lastBucket":"bucket-a",
				"lastObject":"object-a",
				"lastAt":"2026-10-20T10:00:00Z",
				"entries":[{"bucket":"bucket-a","object":"object-a","message":"could not clean source","timestamp":"2026-10-20T10:00:00Z"}]
			},
			"progress":{"objects":10,"versions":12,"bytes":345,"remainingBuckets":2,"bucket":"bucket-a","object":"object-a","elapsed":60,"eta":30}
		},
		{"id":1,"status":"","stopping":false,"used":0.1,"lastError":null,"cleanupWarnings":{"count":0},"progress":null}
	],
	"stoppedAt":null,
	"stopPropagation":{"lastAttemptAt":"2026-10-20T10:01:00Z","failedPeers":["peer-1"],"terminalReloadAttemptAt":null,"terminalReloadFailedPeers":[],"pendingTerminalReload":true}
}`

const rebalanceStatusCompletedJSON = `{
	"id":"` + testRebalanceTriggerID + `",
	"pools":[
		{"id":0,"status":"Completed","stopping":false,"used":0.5,"lastError":null,"cleanupWarnings":{"count":0},"progress":null}
	],
	"stoppedAt":null,
	"stopPropagation":{"lastAttemptAt":null,"failedPeers":[],"terminalReloadAttemptAt":null,"terminalReloadFailedPeers":[],"pendingTerminalReload":false}
}`

const rebalanceStatusStoppedJSON = `{
	"id":"` + testRebalanceTriggerID + `",
	"pools":[
		{"id":0,"status":"Stopped","stopping":false,"used":0.5,"lastError":null,"cleanupWarnings":{"count":0},"progress":null}
	],
	"stoppedAt":"2026-10-20T11:00:00Z",
	"stopPropagation":{"lastAttemptAt":"2026-10-20T11:00:00Z","failedPeers":[],"terminalReloadAttemptAt":null,"terminalReloadFailedPeers":[],"pendingTerminalReload":false}
}`

const rebalanceStatusOtherRunJSON = `{
	"id":"` + testOtherRebalanceTriggerID + `",
	"pools":[
		{"id":0,"status":"Started","stopping":false,"used":0.3,"lastError":null,"cleanupWarnings":{"count":0},"progress":null}
	],
	"stoppedAt":null,
	"stopPropagation":{"lastAttemptAt":null,"failedPeers":[],"terminalReloadAttemptAt":null,"terminalReloadFailedPeers":[],"pendingTerminalReload":false}
}`

// nullRebalanceModel is the model of a resource whose computed attributes have
// not been observed yet. Building the plan from Go zero values would lose the
// element types of the collection attributes.
func nullRebalanceModel() RebalanceResourceModel {
	return RebalanceResourceModel{
		ID:              types.StringNull(),
		Status:          types.StringNull(),
		StoppedAt:       types.StringNull(),
		Pools:           types.ListNull(rebalancePoolsObjectType()),
		StopPropagation: types.ObjectNull(rebalanceStopPropagationAttrTypes),
	}
}

func TestRebalanceResourceSchema(t *testing.T) {
	r := NewRebalanceResource()
	sch := resourceSchemaOf(t, r)

	attrs := sch.GetAttributes()
	if len(attrs) != 5 {
		t.Errorf("expected 5 attributes, got %d: %v", len(attrs), attrs)
	}

	for name, attribute := range attrs {
		if attribute.GetDescription() == "" {
			t.Errorf("%s has no description, tfplugindocs would publish an empty row", name)
		}
	}

	id, ok := attrs["id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("id is not a StringAttribute")
	}
	if id.Required || id.Optional || !id.Computed {
		t.Errorf("id must be Computed only: %+v", id)
	}

	status, ok := attrs["status"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("status is not a StringAttribute")
	}
	if status.Required || status.Optional || !status.Computed {
		t.Errorf("status must be Computed only: %+v", status)
	}

	stoppedAt, ok := attrs["stopped_at"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("stopped_at is not a StringAttribute")
	}
	if stoppedAt.Required || stoppedAt.Optional || !stoppedAt.Computed {
		t.Errorf("stopped_at must be Computed only: %+v", stoppedAt)
	}

	pools, ok := attrs["pools"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("pools is not a ListNestedAttribute")
	}
	if pools.Required || pools.Optional || !pools.Computed {
		t.Errorf("pools must be Computed only: %+v", pools)
	}
	for _, nested := range []string{"progress", "cleanup_warnings"} {
		if _, ok := pools.NestedObject.Attributes[nested].(schema.SingleNestedAttribute); !ok {
			t.Errorf("pools.%s is not a SingleNestedAttribute", nested)
		}
	}

	stopPropagation, ok := attrs["stop_propagation"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("stop_propagation is not a SingleNestedAttribute")
	}
	if stopPropagation.Required || stopPropagation.Optional || !stopPropagation.Computed {
		t.Errorf("stop_propagation must be Computed only: %+v", stopPropagation)
	}
}

func TestRebalanceResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewRebalanceResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_rebalance" {
		t.Errorf("expected rustfs_rebalance, got %s", resp.TypeName)
	}
}

func TestRebalanceResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewRebalanceResource().(*RebalanceResource)
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
		r := NewRebalanceResource().(*RebalanceResource)
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
		client, _ := routedClient(t)
		h := newCRUDHarness(t, NewRebalanceResource(), client)
		if h.res.(*RebalanceResource).client == nil {
			t.Error("client was not set")
		}
	})
}

func TestRebalanceResourceCreate(t *testing.T) {
	t.Run("starts the rebalance and mirrors the report", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "rebalance/start", status: http.StatusOK, body: `{"id":"` + testRebalanceTriggerID + `"}`},
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		resp := h.create(nullRebalanceModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "rebalance/start")
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
		assertStateString(t, resp.State, path.Root("status"), "Started")
	})

	t.Run("a status route that is not there leaves the trigger observable later", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "rebalance/start", status: http.StatusOK, body: `{"id":"` + testRebalanceTriggerID + `"}`},
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "no status route")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		resp := h.create(nullRebalanceModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
	})

	t.Run("a failing status read after a successful start is a warning", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "rebalance/start", status: http.StatusOK, body: `{"id":"` + testRebalanceTriggerID + `"}`},
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "status is down")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		resp := h.create(nullRebalanceModel())

		if resp.Diagnostics.HasError() {
			t.Fatalf("a status read must not fail the create: %v", resp.Diagnostics)
		}
		if len(resp.Diagnostics) != 1 || resp.Diagnostics[0].Severity() != diag.SeverityWarning {
			t.Fatalf("expected one warning, got %v", resp.Diagnostics)
		}
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
	})

	t.Run("reports a missing trigger id", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "rebalance/start", status: http.StatusOK, body: `{}`},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		plan := nullRebalanceModel()
		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting rebalance", "did not return a trigger id")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports a server failure", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "rebalance/start", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "rebalance requires more than one pool")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		resp := h.create(nullRebalanceModel())

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error starting rebalance")
		if !strings.Contains(diagnostic.Detail(), "NotImplemented") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

func TestRebalanceResourceRead(t *testing.T) {
	t.Run("mirrors the live report", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.read(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "rebalance/status")
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
		assertStateString(t, resp.State, path.Root("status"), "Started")

		got := rebalanceStateModel(t, resp.State)
		if !got.StoppedAt.IsNull() {
			t.Errorf("stopped_at = %v, want null", got.StoppedAt)
		}

		pools := rebalancePoolsFromModel(t, got)
		if len(pools) != 2 {
			t.Fatalf("pools = %d, want 2", len(pools))
		}
		first := pools[0]
		if first.ID.ValueInt64() != 0 || first.Status.ValueString() != "Started" || first.Stopping.ValueBool() {
			t.Errorf("unexpected first pool: %+v", first)
		}
		if first.Used.ValueFloat64() != 0.42 {
			t.Errorf("used = %v, want 0.42", first.Used.ValueFloat64())
		}
		if first.LastError.ValueString() != "slow disk" {
			t.Errorf("last_error = %q, want slow disk", first.LastError.ValueString())
		}

		progress := rebalanceProgressFromObject(t, first.Progress)
		if progress.Objects.ValueInt64() != 10 || progress.Versions.ValueInt64() != 12 || progress.Bytes.ValueInt64() != 345 {
			t.Errorf("unexpected progress counts: %+v", progress)
		}
		if progress.RemainingBuckets.ValueInt64() != 2 || progress.Bucket.ValueString() != "bucket-a" || progress.Object.ValueString() != "object-a" {
			t.Errorf("unexpected progress position: %+v", progress)
		}
		if progress.Elapsed.ValueInt64() != 60 || progress.ETA.ValueInt64() != 30 {
			t.Errorf("unexpected progress timing: %+v", progress)
		}

		warnings := rebalanceWarningsFromObject(t, first.CleanupWarnings)
		if warnings.Count.ValueInt64() != 1 || warnings.LastMessage.ValueString() != "could not clean source" {
			t.Errorf("unexpected cleanup warnings: %+v", warnings)
		}
		if warnings.LastBucket.ValueString() != "bucket-a" || warnings.LastObject.ValueString() != "object-a" || warnings.LastAt.ValueString() != "2026-10-20T10:00:00Z" {
			t.Errorf("unexpected cleanup warning position: %+v", warnings)
		}
		entries := rebalanceEntriesFromList(t, warnings.Entries)
		if len(entries) != 1 || entries[0].Bucket.ValueString() != "bucket-a" || entries[0].Timestamp.ValueString() != "2026-10-20T10:00:00Z" {
			t.Errorf("unexpected cleanup warning entries: %+v", entries)
		}

		if !pools[1].Progress.IsNull() {
			t.Errorf("inactive pool progress = %v, want null", pools[1].Progress)
		}
		if pools[1].Status.ValueString() != "" {
			t.Errorf("inactive pool status = %q, want empty", pools[1].Status.ValueString())
		}

		stopPropagation := rebalanceStopPropagationFromObject(t, got.StopPropagation)
		if stopPropagation.LastAttemptAt.ValueString() != "2026-10-20T10:01:00Z" {
			t.Errorf("last_attempt_at = %q", stopPropagation.LastAttemptAt.ValueString())
		}
		if !stopPropagation.PendingTerminalReload.ValueBool() {
			t.Error("pending_terminal_reload = false, want true")
		}
		if peers := rebalanceListStrings(t, stopPropagation.FailedPeers); len(peers) != 1 || peers[0] != "peer-1" {
			t.Errorf("failed_peers = %v, want [peer-1]", peers)
		}
	})

	t.Run("mirrors a stopped run", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusStoppedJSON},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.read(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("status"), "Stopped")
		assertStateString(t, resp.State, path.Root("stopped_at"), "2026-10-20T11:00:00Z")
	})

	t.Run("adopts the legacy fixed id", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(legacyRebalanceID)
		resp := h.read(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
	})

	t.Run("keeps the recorded trigger when status reports another run", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusOtherRunJSON},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		state.Status = types.StringValue("Completed")
		resp := h.read(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
		assertStateString(t, resp.State, path.Root("status"), "Completed")
	})

	t.Run("an unsupported status route keeps the state without failing", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "no status route")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue("rebalance-triggered")
		resp := h.read(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("id"), "rebalance-triggered")
	})

	t.Run("a missing rebalance record keeps the recorded trigger without failing", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "pool rebalance is not started")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue("rebalance-triggered")
		resp := h.read(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("id"), "rebalance-triggered")
	})

	t.Run("a bare 404 is treated as an unimplemented route", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusNotFound, body: ""},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.read(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
	})

	t.Run("a structured 404 that is not a missing record is reported", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusNotFound, body: s3ErrorXML("NoSuchKey", "wrong endpoint")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.read(state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading rebalance", "NoSuchKey")
	})

	t.Run("a server error is reported", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue("rebalance-triggered")
		resp := h.read(state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading rebalance", "Could not read rebalance status")
	})
}

func TestRebalanceResourceUpdate(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewRebalanceResource(), client)

	plan := nullRebalanceModel()
	plan.ID = types.StringValue(testRebalanceTriggerID)
	plan.Status = types.StringValue("Started")
	resp := h.update(plan, nullRebalanceModel())

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
	if got := len(rec.requests); got != 0 {
		t.Errorf("Update must not call the admin API, saw %d requests", got)
	}
}

func TestRebalanceResourceDelete(t *testing.T) {
	t.Run("stops a running rebalance", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
			requestRoute{method: http.MethodPost, pathContains: "rebalance/stop", status: http.StatusOK},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "rebalance/stop")
	})

	t.Run("a terminal rebalance is not stopped", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusCompletedJSON},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "rebalance/stop")
	})

	t.Run("a rebalance that is already stopping is not stopped again", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: `{"id":"` + testRebalanceTriggerID + `","pools":[{"id":0,"status":"Started","stopping":true,"used":0.4,"cleanupWarnings":{"count":0}}],"stopPropagation":{"failedPeers":[],"terminalReloadFailedPeers":[]}}`},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "rebalance/stop")
	})

	t.Run("an unsupported status route warns and does not stop", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "no status route")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		if resp.Diagnostics.HasError() {
			t.Fatalf("an unsupported status route must not fail destroy: %v", resp.Diagnostics)
		}
		if len(resp.Diagnostics) != 1 || resp.Diagnostics[0].Severity() != diag.SeverityWarning {
			t.Fatalf("expected one warning, got %v", resp.Diagnostics)
		}
		rec.assertNoRequest(t, "rebalance/stop")
	})

	t.Run("leaves a rebalance started outside Terraform alone", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusOtherRunJSON},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if len(resp.Diagnostics) != 1 || resp.Diagnostics[0].Severity() != diag.SeverityWarning {
			t.Fatalf("expected one warning, got %v", resp.Diagnostics)
		}
		rec.assertNoRequest(t, "rebalance/stop")
	})

	t.Run("a stop that loses the race to a terminal run succeeds", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
			requestRoute{method: http.MethodPost, pathContains: "rebalance/stop", status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "pool rebalance is not started")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "rebalance/stop")
	})

	t.Run("a missing rebalance record is an idempotent no-op", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "pool rebalance is not started")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "rebalance/stop")
	})

	t.Run("an unsupported stop route warns but does not fail destroy", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
			requestRoute{method: http.MethodPost, pathContains: "rebalance/stop", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "no stop route")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		if resp.Diagnostics.HasError() {
			t.Fatalf("an unsupported stop route must not fail destroy: %v", resp.Diagnostics)
		}
		if len(resp.Diagnostics) != 1 || resp.Diagnostics[0].Severity() != diag.SeverityWarning {
			t.Fatalf("expected one warning about the rebalance still running, got %v", resp.Diagnostics)
		}
		rec.assertRequest(t, "rebalance/stop")
	})

	t.Run("a stop failure is reported", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
			requestRoute{method: http.MethodPost, pathContains: "rebalance/stop", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "stop failed")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error stopping rebalance", "Could not stop rebalance")
	})
}

func TestRebalanceResourceImportState(t *testing.T) {
	r := NewRebalanceResource().(*RebalanceResource)
	sch := resourceSchemaOf(t, r)

	resp := &frameworkresource.ImportStateResponse{State: resourceState(t, sch, nullRebalanceModel())}
	r.ImportState(context.Background(), frameworkresource.ImportStateRequest{ID: testRebalanceTriggerID}, resp)

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
}

func TestRebalanceOverallStatus(t *testing.T) {
	t.Parallel()

	stoppedAt := "2026-10-20T11:00:00Z"
	pool := func(status string, stopping bool) rustfs.RebalancePoolStatus {
		return rustfs.RebalancePoolStatus{Status: status, Stopping: stopping}
	}

	cases := []struct {
		name   string
		report rustfs.RebalanceReport
		want   string
	}{
		{"active pool wins", rustfs.RebalanceReport{Pools: []rustfs.RebalancePoolStatus{pool("Started", false)}}, "Started"},
		{"stopping pool is still active", rustfs.RebalanceReport{Pools: []rustfs.RebalancePoolStatus{pool("Completed", true)}}, "Started"},
		{"failed pool", rustfs.RebalanceReport{Pools: []rustfs.RebalancePoolStatus{pool("Failed", false)}}, "Failed"},
		{"stopped by timestamp", rustfs.RebalanceReport{StoppedAt: &stoppedAt, Pools: []rustfs.RebalancePoolStatus{pool("Stopped", false)}}, "Stopped"},
		{"stopped without a timestamp", rustfs.RebalanceReport{Pools: []rustfs.RebalancePoolStatus{pool("Stopped", false)}}, "Stopped"},
		{"completed pool", rustfs.RebalanceReport{Pools: []rustfs.RebalancePoolStatus{pool("Completed", false)}}, "Completed"},
		{"no pools", rustfs.RebalanceReport{}, "None"},
		{"failure outranks a stop timestamp", rustfs.RebalanceReport{StoppedAt: &stoppedAt, Pools: []rustfs.RebalancePoolStatus{pool("Failed", false)}}, "Failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := overallRebalanceStatus(tc.report); got != tc.want {
				t.Errorf("overallRebalanceStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Rebalance acceptance coverage. POST /rustfs/admin/v3/rebalance/start is
// registered on RustFS 1.0.x, but only a deployment with two or more pools can
// actually rebalance: on the single-pool CI deployment the route answers 501
// NotImplemented. The precheck therefore probes the pool list and skips when
// the deployment has nothing to rebalance.

func TestAccRebalanceResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccRebalancePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `resource "rustfs_rebalance" "test" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(rebalanceResourceName, "id"),
					resource.TestCheckResourceAttrSet(rebalanceResourceName, "status"),
				),
			},
			{
				// The trigger id is the import id. The live progress attributes
				// move while the rebalance runs, so only the id is compared.
				ResourceName:            rebalanceResourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"status", "pools", "stopped_at", "stop_propagation"},
			},
		},
	})
}

// testAccRebalancePreCheck skips the test unless the deployment has more than
// one pool: a single-pool deployment has nothing to rebalance and the server
// answers 501 NotImplemented. A failed probe cannot confirm the pool count, so
// it skips too, carrying the error into the skip message; any other failure is
// left to fail the test.
func testAccRebalancePreCheck(t *testing.T) {
	testAccPreCheck(t)

	client := testAccRustClient()
	var (
		pools []rustfs.PoolInfo
		err   error
	)
	pools, err = client.ListPools(context.Background())
	if err != nil {
		t.Skipf("could not list pools to confirm the deployment can rebalance: %v", err)
	}
	if len(pools) < 2 {
		t.Skipf("deployment has %d pool(s): a single-pool deployment has nothing to rebalance and POST /rustfs/admin/v3/rebalance/start answers 501 NotImplemented", len(pools))
	}
}

// TestRebalanceResourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: the live status report, a status route the
// server does not implement, a stop on a terminal run, and a server that cannot
// stop.
func TestRebalanceResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("read: mirrors the live rebalance report", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON})
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		resp := h.read(nullRebalanceModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
		assertStateString(t, resp.State, path.Root("status"), "Started")
		rec.assertRequest(t, "rebalance/status")
	})

	t.Run("read: status unsupported keeps the recorded trigger", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "no status route")})
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue("rebalance-triggered")
		resp := h.read(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("id"), "rebalance-triggered")
	})

	t.Run("delete: stops a running rebalance", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
			requestRoute{method: http.MethodPost, pathContains: "rebalance/stop", status: http.StatusOK},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "rebalance/stop")
	})

	t.Run("delete: a terminal rebalance is not stopped", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusCompletedJSON})
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "rebalance/stop")
	})

	t.Run("delete: an unsupported stop route warns and succeeds", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
			requestRoute{method: http.MethodPost, pathContains: "rebalance/stop", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "no stop route")},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		if resp.Diagnostics.HasError() {
			t.Fatalf("an unsupported stop route must not fail destroy: %v", resp.Diagnostics)
		}
		if len(resp.Diagnostics) != 1 || resp.Diagnostics[0].Severity() != diag.SeverityWarning {
			t.Fatalf("expected one warning, got %v", resp.Diagnostics)
		}
		rec.assertRequest(t, "rebalance/stop")
	})

	t.Run("delete: a missing record is an idempotent no-op", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "pool rebalance is not started")})
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		state := nullRebalanceModel()
		state.ID = types.StringValue(testRebalanceTriggerID)
		resp := h.delete(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "rebalance/stop")
	})

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed live on RustFS latest: POST rebalance/start answers 501
			// NotImplemented.
			name:        "server that cannot rebalance reports the rejection and writes no state",
			routes:      []requestRoute{{method: http.MethodPost, pathContains: "rebalance/start", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "rebalance on a single-drive pool is not implemented")}},
			wantSummary: "Error starting rebalance",
			wantDetail:  "Could not start rebalance: ",
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{method: http.MethodPost, pathContains: "rebalance/start", status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error starting rebalance",
			wantDetail:  "Could not start rebalance: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewRebalanceResource(), client)
			plan := nullRebalanceModel()

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("create: accepted start records the trigger id", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "rebalance/start", status: http.StatusOK, body: `{"id":"` + testRebalanceTriggerID + `"}`},
			requestRoute{method: http.MethodGet, pathContains: "rebalance/status", status: http.StatusOK, body: rebalanceStatusJSON},
		)
		h := newCRUDHarness(t, NewRebalanceResource(), client)

		resp := h.create(nullRebalanceModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
		rec.assertRequest(t, "rebalance/start")
	})

	t.Run("import: the trigger id is the import id", func(t *testing.T) {
		h := newCRUDHarness(t, NewRebalanceResource(), cannedClient(t, http.StatusOK, ""))
		importer, ok := h.res.(frameworkresource.ResourceWithImportState)
		if !ok {
			t.Fatal("resource does not implement import")
		}

		resp := &frameworkresource.ImportStateResponse{State: h.state(nullRebalanceModel())}
		importer.ImportState(context.Background(), frameworkresource.ImportStateRequest{ID: testRebalanceTriggerID}, resp)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), testRebalanceTriggerID)
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewRebalanceResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

// rebalanceStateModel reads the whole resource state so nested report values
// can be asserted, not just the scalar summary.
func rebalanceStateModel(t *testing.T, state tfsdk.State) RebalanceResourceModel {
	t.Helper()

	var got RebalanceResourceModel
	if diags := state.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	return got
}

func rebalancePoolsFromModel(t *testing.T, model RebalanceResourceModel) []RebalancePoolModel {
	t.Helper()

	var pools []RebalancePoolModel
	if diags := model.Pools.ElementsAs(context.Background(), &pools, false); diags.HasError() {
		t.Fatalf("reading pools: %v", diags)
	}
	return pools
}

func rebalanceProgressFromObject(t *testing.T, object types.Object) RebalanceProgressModel {
	t.Helper()

	var progress RebalanceProgressModel
	if diags := object.As(context.Background(), &progress, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("reading progress: %v", diags)
	}
	return progress
}

func rebalanceWarningsFromObject(t *testing.T, object types.Object) RebalanceCleanupWarningsModel {
	t.Helper()

	var warnings RebalanceCleanupWarningsModel
	if diags := object.As(context.Background(), &warnings, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("reading cleanup warnings: %v", diags)
	}
	return warnings
}

func rebalanceEntriesFromList(t *testing.T, list types.List) []RebalanceCleanupWarningEntryModel {
	t.Helper()

	var entries []RebalanceCleanupWarningEntryModel
	if diags := list.ElementsAs(context.Background(), &entries, false); diags.HasError() {
		t.Fatalf("reading cleanup warning entries: %v", diags)
	}
	return entries
}

func rebalanceStopPropagationFromObject(t *testing.T, object types.Object) RebalanceStopPropagationModel {
	t.Helper()

	var stopPropagation RebalanceStopPropagationModel
	if diags := object.As(context.Background(), &stopPropagation, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("reading stop propagation: %v", diags)
	}
	return stopPropagation
}

func rebalanceListStrings(t *testing.T, list types.List) []string {
	t.Helper()

	var values []string
	if diags := list.ElementsAs(context.Background(), &values, false); diags.HasError() {
		t.Fatalf("reading strings: %v", diags)
	}
	return values
}
