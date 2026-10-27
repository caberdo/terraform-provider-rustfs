// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	decommissionPoolA = "/data/p1{1...2}"
	decommissionPoolB = "/data/p2{1...2}"

	decommissionRunningInfo = `{"startTime":"2026-10-20T12:52:24.324482583Z","startSize":50827755520,` +
		`"totalSize":63728975872,"currentSize":50827755520,"complete":false,"failed":false,` +
		`"canceled":false,"queued":false,"queuedBuckets":["drainbucket"],` +
		`"decommissionedBuckets":[],"bucket":".rustfs.sys","prefix":"ilm",` +
		`"object":"config/config.json","stage":"source_cleanup","objectsDecommissioned":1,` +
		`"objectsDecommissionedFailed":0,"bytesDecommissioned":107,` +
		`"bytesDecommissionedFailed":0,"waitingReason":null}`

	decommissionCanceledInfo = `{"startTime":null,"startSize":49619329024,"totalSize":63728975872,` +
		`"currentSize":49485033472,"complete":false,"failed":false,"canceled":true,"queued":false,` +
		`"queuedBuckets":["drainbucket"],"decommissionedBuckets":[".rustfs.sys/config"],` +
		`"bucket":".rustfs.sys","prefix":"ilm","object":"buckets/drainbucket/.bucket-incarnation",` +
		`"stage":"entry_finished","objectsDecommissioned":5,"objectsDecommissionedFailed":0,` +
		`"bytesDecommissioned":3728,"bytesDecommissionedFailed":0,"waitingReason":null}`

	decommissionCompleteInfo = `{"startTime":"2026-10-20T12:50:38.839964249Z","startSize":51901947904,` +
		`"totalSize":63728975872,"currentSize":51901837312,"complete":true,"failed":false,` +
		`"canceled":false,"queued":false,"queuedBuckets":[],` +
		`"decommissionedBuckets":[".rustfs.sys/config",".rustfs.sys/buckets",".rustfs.sys/ilm"],` +
		`"bucket":".rustfs.sys","prefix":"ilm","object":"buckets/.usage.v2.json","stage":"",` +
		`"objectsDecommissioned":5,"objectsDecommissionedFailed":0,` +
		`"bytesDecommissioned":3504,"bytesDecommissionedFailed":0,"waitingReason":null}`
)

func decommissionPoolBody(id int, cmdline, status, poolStatus, info string) string {
	return fmt.Sprintf(`{"id":%d,"cmdline":%q,"status":%q,"poolStatus":%q,"decommissionInfo":%s}`,
		id, cmdline, status, poolStatus, info)
}

func decommissionStatusBody(pools ...string) string {
	return `{"pools":[` + strings.Join(pools, ",") + `]}`
}

func TestPoolDecommissionResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewPoolDecommissionResource())
	attrs := sch.GetAttributes()

	for _, name := range []string{
		"id", "pool", "confirm", "status", "pool_status", "started_at", "complete", "failed",
		"canceled", "queued", "queued_buckets", "decommissioned_buckets", "bucket", "prefix",
		"object", "stage", "waiting_reason", "start_size", "total_size", "current_size",
		"objects_decommissioned", "objects_decommissioned_failed", "bytes_decommissioned",
		"bytes_decommissioned_failed",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	pool, ok := attrs["pool"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("pool is %T, want schema.StringAttribute", attrs["pool"])
	}
	if !pool.Required {
		t.Error("pool must be required")
	}
	if !hasRequiresReplace(pool.PlanModifiers) {
		t.Error("pool must carry RequiresReplace: a drain target is bound to one pool")
	}

	confirm, ok := attrs["confirm"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("confirm is %T, want schema.StringAttribute", attrs["confirm"])
	}
	if !confirm.Required {
		t.Error("confirm must be required: starting a drain moves data")
	}

	for name, attr := range attrs {
		if name == "pool" || name == "confirm" {
			continue
		}
		if !attr.IsComputed() {
			t.Errorf("%s must be computed", name)
		}
	}

	for _, name := range []string{"queued_buckets", "decommissioned_buckets"} {
		list, ok := attrs[name].(schema.ListAttribute)
		if !ok {
			t.Fatalf("%s is %T, want schema.ListAttribute", name, attrs[name])
		}
		if list.ElementType != types.StringType {
			t.Errorf("%s element type = %v, want string", name, list.ElementType)
		}
	}
}

func TestPoolDecommissionResourceMetadata(t *testing.T) {
	resp := &resource.MetadataResponse{}
	NewPoolDecommissionResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_pool_decommission" {
		t.Errorf("expected rustfs_pool_decommission, got %s", resp.TypeName)
	}
}

func poolDecommissionPlan(pool string) *PoolDecommissionResourceModel {
	return &PoolDecommissionResourceModel{
		ID:                          types.StringNull(),
		Pool:                        types.StringValue(pool),
		Confirm:                     types.StringValue(pool),
		Status:                      types.StringNull(),
		PoolStatus:                  types.StringNull(),
		StartedAt:                   types.StringNull(),
		Complete:                    types.BoolNull(),
		Failed:                      types.BoolNull(),
		Canceled:                    types.BoolNull(),
		Queued:                      types.BoolNull(),
		QueuedBuckets:               types.ListNull(types.StringType),
		DecommissionedBuckets:       types.ListNull(types.StringType),
		Bucket:                      types.StringNull(),
		Prefix:                      types.StringNull(),
		Object:                      types.StringNull(),
		Stage:                       types.StringNull(),
		WaitingReason:               types.StringNull(),
		StartSize:                   types.Int64Null(),
		TotalSize:                   types.Int64Null(),
		CurrentSize:                 types.Int64Null(),
		ObjectsDecommissioned:       types.Int64Null(),
		ObjectsDecommissionedFailed: types.Int64Null(),
		BytesDecommissioned:         types.Int64Null(),
		BytesDecommissionedFailed:   types.Int64Null(),
	}
}

func poolDecommissionState(pool string) *PoolDecommissionResourceModel {
	model := poolDecommissionPlan(pool)
	model.ID = types.StringValue(pool)
	return model
}

// scriptedResponse is one expected request and the answer it gets. Every
// request is consumed in order, so a test can drive a resource through a
// sequence of status reads and mutations.
type scriptedResponse struct {
	method       string
	pathContains string
	queryPool    string
	status       int
	body         string
}

type scriptedServer struct {
	t         *testing.T
	responses []scriptedResponse
	index     int
	requests  []string
}

func (s *scriptedServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests = append(s.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	if s.index >= len(s.responses) {
		s.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	want := s.responses[s.index]
	s.index++
	if want.method != "" && want.method != r.Method {
		s.t.Errorf("request %d method = %s, want %s", s.index, r.Method, want.method)
	}
	if want.pathContains != "" && !strings.Contains(r.URL.Path, want.pathContains) {
		s.t.Errorf("request %d path = %s, want it to contain %s", s.index, r.URL.Path, want.pathContains)
	}
	if want.queryPool != "" && r.URL.Query().Get("pool") != want.queryPool {
		s.t.Errorf("request %d pool query = %q, want %q", s.index, r.URL.Query().Get("pool"), want.queryPool)
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(want.status)
	_, _ = fmt.Fprint(w, want.body)
}

func scriptedClient(t *testing.T, responses ...scriptedResponse) (*AllClient, *scriptedServer) {
	t.Helper()
	server := &scriptedServer{t: t, responses: responses}
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)
	server.responses = responses
	return newUnitAllClient(srv), server
}

func (s *scriptedServer) assertAllConsumed(t *testing.T) {
	t.Helper()
	if s.index != len(s.responses) {
		t.Fatalf("served %d of %d scripted responses: %v", s.index, len(s.responses), s.requests)
	}
}

func TestPoolDecommissionResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewPoolDecommissionResource().(*PoolDecommissionResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewPoolDecommissionResource().(*PoolDecommissionResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient")
		if r.client != nil {
			t.Error("client must stay unset for unexpected provider data")
		}
	})

	t.Run("all client", func(t *testing.T) {
		r := NewPoolDecommissionResource().(*PoolDecommissionResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: cannedClient(t, http.StatusOK, "")}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if r.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestPoolDecommissionUpdate(t *testing.T) {
	h := newCRUDHarness(t, NewPoolDecommissionResource(), cannedClient(t, http.StatusOK, ""))
	plan := poolDecommissionPlan(decommissionPoolB)
	plan.Status = types.StringValue("running")

	resp := h.update(plan, poolDecommissionState(decommissionPoolB))

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("status"), "running")
}

func TestPoolDecommissionValidateConfig(t *testing.T) {
	h := newCRUDHarness(t, NewPoolDecommissionResource(), cannedClient(t, http.StatusOK, decommissionStatusBody()))

	t.Run("matching confirmation passes", func(t *testing.T) {
		resp := h.validateConfig(poolDecommissionPlan(decommissionPoolB))
		requireNoDiagnostics(t, resp.Diagnostics)
	})

	t.Run("mismatched confirmation is rejected", func(t *testing.T) {
		model := poolDecommissionPlan(decommissionPoolB)
		model.Confirm = types.StringValue(decommissionPoolA)
		resp := h.validateConfig(model)
		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid pool decommission confirmation",
			"confirm must repeat pool exactly ("+decommissionPoolB+")")
	})

	t.Run("unknown pool defers validation", func(t *testing.T) {
		model := poolDecommissionPlan(decommissionPoolB)
		model.Pool = types.StringUnknown()
		model.Confirm = types.StringUnknown()
		resp := h.validateConfig(model)
		requireNoDiagnostics(t, resp.Diagnostics)
	})
}

func TestPoolDecommissionCreate(t *testing.T) {
	t.Run("unknown pool lists the current pools", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
					decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)
		plan := poolDecommissionPlan("/missing{1...2}")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting pool decommission",
			`does not report a storage pool named "/missing{1...2}"`)
		assertDiagnosticDetail(t, resp.Diagnostics, decommissionPoolA)
		h.assertNoPartialState(resp, plan)
		server.assertAllConsumed(t)
	})

	t.Run("already running is adopted without a second start", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.create(poolDecommissionPlan(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), decommissionPoolB)
		assertStateString(t, resp.State, path.Root("status"), "running")
		assertStateString(t, resp.State, path.Root("pool_status"), "decommissioning")
		assertStateString(t, resp.State, path.Root("queued_buckets").AtListIndex(0), "drainbucket")
		server.assertAllConsumed(t)
	})

	t.Run("completed drain is adopted", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
					decommissionPoolBody(1, decommissionPoolB, "complete", "decommissioned", decommissionCompleteInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.create(poolDecommissionPlan(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("status"), "complete")
		assertStateString(t, resp.State, path.Root("pool_status"), "decommissioned")
		var complete types.Bool
		if diags := resp.State.GetAttribute(context.Background(), path.Root("complete"), &complete); diags.HasError() {
			t.Fatalf("complete attribute: %v", diags)
		}
		if !complete.ValueBool() {
			t.Error("complete must be true")
		}
		server.assertAllConsumed(t)
	})

	t.Run("none starts the drain and re-reads its progress", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
					decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/decommission", queryPool: decommissionPoolB, status: http.StatusOK},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.create(poolDecommissionPlan(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("status"), "running")
		assertStateString(t, resp.State, path.Root("started_at"), "2026-10-20T12:52:24.324482583Z")
		server.assertAllConsumed(t)
	})

	t.Run("started drain that cannot be re-read is reported", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/decommission", queryPool: decommissionPoolB, status: http.StatusOK},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusForbidden,
				body: s3ErrorXML("AccessDenied", "nope")},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)
		plan := poolDecommissionPlan(decommissionPoolB)

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading pool decommission",
			"Could not read the decommission of pool "+fmt.Sprintf("%q", decommissionPoolB))
		h.assertNoPartialState(resp, plan)
		server.assertAllConsumed(t)
	})

	t.Run("started drain with no record is reported", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/decommission", queryPool: decommissionPoolB, status: http.StatusOK},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)
		plan := poolDecommissionPlan(decommissionPoolB)

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading pool decommission",
			"does not report a decommission record for it")
		h.assertNoPartialState(resp, plan)
		server.assertAllConsumed(t)
	})

	t.Run("unknown status is refused", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "paused", "blocked", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.create(poolDecommissionPlan(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting pool decommission",
			`reports the unknown decommission status "paused"`)
		server.assertAllConsumed(t)
	})

	t.Run("failed start leaves no partial state", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/decommission", queryPool: decommissionPoolB,
				status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)
		plan := poolDecommissionPlan(decommissionPoolB)

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting pool decommission",
			"Could not start a decommission of pool "+fmt.Sprintf("%q", decommissionPoolB))
		h.assertNoPartialState(resp, plan)
		server.assertAllConsumed(t)
	})

	t.Run("failed record must be cleared first", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "failed", "blocked", decommissionCanceledInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.create(poolDecommissionPlan(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting pool decommission",
			`already carries a failed decommission record`)
		server.assertAllConsumed(t)
	})

	t.Run("confirmation mismatch is refused before any request", func(t *testing.T) {
		client, server := scriptedClient(t)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)
		plan := poolDecommissionPlan(decommissionPoolB)
		plan.Confirm = types.StringValue(decommissionPoolA)

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid pool decommission confirmation",
			"confirm must repeat pool exactly ("+decommissionPoolB+")")
		server.assertAllConsumed(t)
	})

	t.Run("single pool 501 explains the topology requirement", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusNotImplemented,
				body: `<Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented</Message></Error>`},
			scriptedResponse{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
			scriptedResponse{method: http.MethodGet, pathContains: "pools/list", status: http.StatusOK,
				body: `[{"id":0,"cmdline":"/data","status":"active"}]`},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.create(poolDecommissionPlan("/data"))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting pool decommission",
			"only on a deployment with at least two storage pools")
		assertDiagnosticDetail(t, resp.Diagnostics, `"/data"`)
		server.assertAllConsumed(t)
	})

	t.Run("501 with no reported pools falls back to the raw error", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusNotImplemented,
				body: `<Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented</Message></Error>`},
			scriptedResponse{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
			scriptedResponse{method: http.MethodGet, pathContains: "pools/list", status: http.StatusOK, body: `[]`},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.create(poolDecommissionPlan("/data"))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting pool decommission", "does not implement")
		server.assertAllConsumed(t)
	})

	t.Run("unknown pool with an empty inventory names the empty list", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody()},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.create(poolDecommissionPlan(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting pool decommission",
			"Current pools: no pools.")
		server.assertAllConsumed(t)
	})

	t.Run("multi-pool 501 is reported as an unsupported route", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusNotImplemented,
				body: `<Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented</Message></Error>`},
			scriptedResponse{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
			scriptedResponse{method: http.MethodGet, pathContains: "pools/list", status: http.StatusOK,
				body: `[{"id":0,"cmdline":"/data/p1{1...2}"},{"id":1,"cmdline":"/data/p2{1...2}"}]`},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.create(poolDecommissionPlan(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting pool decommission", "does not implement")
		assertDiagnosticDetail(t, resp.Diagnostics, "POST /rustfs/admin/v3/pools/decommission")
		server.assertAllConsumed(t)
	})
}

func TestPoolDecommissionRead(t *testing.T) {
	t.Run("running drain mirrors status and progress", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.read(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), decommissionPoolB)
		assertStateString(t, resp.State, path.Root("confirm"), decommissionPoolB)
		assertStateString(t, resp.State, path.Root("status"), "running")
		assertStateString(t, resp.State, path.Root("object"), "config/config.json")
		var objects types.Int64
		if diags := resp.State.GetAttribute(context.Background(), path.Root("objects_decommissioned"), &objects); diags.HasError() {
			t.Fatalf("objects_decommissioned: %v", diags)
		}
		if objects.ValueInt64() != 1 {
			t.Errorf("objects_decommissioned = %d, want 1", objects.ValueInt64())
		}
		server.assertAllConsumed(t)
	})

	t.Run("missing pool is removed from state", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.read(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
		server.assertAllConsumed(t)
	})

	t.Run("cleared record is removed from state", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.read(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
		server.assertAllConsumed(t)
	})

	t.Run("unrelated failure keeps state and names the call", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")})
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.read(poolDecommissionState(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading pool decommission",
			"Could not read the decommission of pool "+fmt.Sprintf("%q", decommissionPoolB))
		assertStatePresent(t, resp.State)
	})

	t.Run("single pool 501 explains the topology requirement", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusNotImplemented,
				body: `<Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented</Message></Error>`},
			scriptedResponse{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
			scriptedResponse{method: http.MethodGet, pathContains: "pools/list", status: http.StatusOK,
				body: `[{"id":0,"cmdline":"/data","status":"active"}]`},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.read(poolDecommissionState("/data"))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading pool decommission",
			"only on a deployment with at least two storage pools")
		assertStatePresent(t, resp.State)
		server.assertAllConsumed(t)
	})
}

func TestPoolDecommissionDelete(t *testing.T) {
	t.Run("missing pool needs no mutation", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		server.assertAllConsumed(t)
	})

	t.Run("cleared pool needs no mutation", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		server.assertAllConsumed(t)
	})

	t.Run("completed drain is terminal and is not canceled", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "complete", "decommissioned", decommissionCompleteInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		server.assertAllConsumed(t)
	})

	t.Run("running drain is canceled and its record cleared", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB, status: http.StatusOK},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "canceled", "blocked", decommissionCanceledInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/clear", queryPool: decommissionPoolB, status: http.StatusOK},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		server.assertAllConsumed(t)
	})

	t.Run("cancel races a completion and stays successful", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB,
				status: http.StatusBadRequest,
				body:   s3ErrorXML("InvalidRequest", "admin cancel decommission failed for pool 1: Invalid Request")},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "complete", "decommissioned", decommissionCompleteInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		server.assertAllConsumed(t)
	})

	t.Run("failed record is cleared", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "failed", "blocked", decommissionCanceledInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/clear", queryPool: decommissionPoolB, status: http.StatusOK},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		server.assertAllConsumed(t)
	})

	t.Run("cancel race against a still-running drain is reported", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB,
				status: http.StatusBadRequest,
				body:   s3ErrorXML("InvalidRequest", "admin cancel decommission failed for pool 1: Invalid Request")},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error canceling pool decommission",
			"Invalid Request")
		server.assertAllConsumed(t)
	})

	t.Run("clear failure on an already-cleared record is not an error", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "canceled", "blocked", decommissionCanceledInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/clear", queryPool: decommissionPoolB,
				status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		server.assertAllConsumed(t)
	})

	t.Run("status read failure is reported", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")})
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error canceling pool decommission",
			"Could not cancel the decommission of pool "+fmt.Sprintf("%q", decommissionPoolB))
	})

	t.Run("cancel race whose re-read fails is reported", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB,
				status: http.StatusBadRequest,
				body:   s3ErrorXML("InvalidRequest", "admin cancel decommission failed for pool 1: Invalid Request")},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusForbidden,
				body: s3ErrorXML("AccessDenied", "nope")},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error canceling pool decommission",
			"Could not cancel the decommission of pool "+fmt.Sprintf("%q", decommissionPoolB))
		server.assertAllConsumed(t)
	})

	t.Run("cancel race that lands on a canceled record clears it", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB,
				status: http.StatusBadRequest,
				body:   s3ErrorXML("InvalidRequest", "admin cancel decommission failed for pool 1: Invalid Request")},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "canceled", "blocked", decommissionCanceledInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/clear", queryPool: decommissionPoolB, status: http.StatusOK},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		server.assertAllConsumed(t)
	})

	t.Run("post-cancel re-read failure is reported", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB, status: http.StatusOK},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusForbidden,
				body: s3ErrorXML("AccessDenied", "nope")},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error clearing pool decommission",
			"Could not clear the decommission of pool "+fmt.Sprintf("%q", decommissionPoolB))
		server.assertAllConsumed(t)
	})

	t.Run("post-cancel missing record needs no clear", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB, status: http.StatusOK},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		server.assertAllConsumed(t)
	})

	t.Run("unknown status is refused", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "paused", "blocked", "null"),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error canceling pool decommission",
			`reports the unknown decommission status "paused"`)
		server.assertAllConsumed(t)
	})

	t.Run("cancel failure is reported", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB,
				status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error canceling pool decommission",
			"Could not cancel the decommission of pool "+fmt.Sprintf("%q", decommissionPoolB))
		server.assertAllConsumed(t)
	})

	t.Run("clear failure is reported when the record is still there", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "canceled", "blocked", decommissionCanceledInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/clear", queryPool: decommissionPoolB,
				status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "canceled", "blocked", decommissionCanceledInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error clearing pool decommission",
			"Could not clear the decommission of pool "+fmt.Sprintf("%q", decommissionPoolB))
		server.assertAllConsumed(t)
	})

	t.Run("cancel accepted but still running is reported", func(t *testing.T) {
		client, server := scriptedClient(t,
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
			scriptedResponse{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB, status: http.StatusOK},
			scriptedResponse{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
				body: decommissionStatusBody(
					decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
				)},
		)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

		resp := h.delete(poolDecommissionState(decommissionPoolB))

		assertErrorDiagnostic(t, resp.Diagnostics, "Error canceling pool decommission",
			"still reports \"running\"")
		server.assertAllConsumed(t)
	})
}

// TestPoolDecommissionResourceLifecyclePaths pins the cross-method contract in
// one place: a create writes the state a refresh can consume, a failed create
// writes nothing, a drain the server no longer reports is removed from state,
// and destroy is idempotent for an absent or completed drain while still
// surfacing the topology gap that must not be swallowed.
func TestPoolDecommissionResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("configure rejects unexpected provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewPoolDecommissionResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient")
	})

	createCases := []struct {
		name        string
		pool        string
		responses   []scriptedResponse
		wantStatus  string
		wantSummary string
		wantDetail  string
	}{
		{
			name: "none starts the drain and records the re-read progress",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
					)},
				{method: http.MethodPost, pathContains: "pools/decommission", queryPool: decommissionPoolB, status: http.StatusOK},
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
					)},
			},
			wantStatus: "running",
		},
		{
			name: "an existing drain is adopted without a second start",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "complete", "decommissioned", decommissionCompleteInfo),
					)},
			},
			wantStatus: "complete",
		},
		{
			name: "an unknown pool names the current pools and writes no state",
			pool: "/missing{1...2}",
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
					)},
			},
			wantSummary: "Error starting pool decommission",
			wantDetail:  `does not report a storage pool named "/missing{1...2}"`,
		},
		{
			name: "a failed start writes no state",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
					)},
				{method: http.MethodPost, pathContains: "pools/decommission", queryPool: decommissionPoolB,
					status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")},
			},
			wantSummary: "Error starting pool decommission",
			wantDetail:  "Could not start a decommission of pool " + fmt.Sprintf("%q", decommissionPoolB),
		},
		{
			name: "a single-pool deployment explains the topology requirement",
			pool: "/data",
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusNotImplemented,
					body: `<Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented</Message></Error>`},
				{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
				{method: http.MethodGet, pathContains: "pools/list", status: http.StatusOK,
					body: `[{"id":0,"cmdline":"/data","status":"active"}]`},
			},
			wantSummary: "Error starting pool decommission",
			wantDetail:  "only on a deployment with at least two storage pools",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, server := scriptedClient(t, tc.responses...)
			h := newCRUDHarness(t, NewPoolDecommissionResource(), client)
			plan := poolDecommissionPlan(tc.pool)

			resp := h.create(plan)

			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				h.assertNoPartialState(resp, plan)
				server.assertAllConsumed(t)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)
			assertStateString(t, resp.State, path.Root("id"), tc.pool)
			assertStateString(t, resp.State, path.Root("pool"), tc.pool)
			assertStateString(t, resp.State, path.Root("status"), tc.wantStatus)
			server.assertAllConsumed(t)
		})
	}

	readCases := []struct {
		name        string
		pool        string
		responses   []scriptedResponse
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name: "a running drain is mirrored into state",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
					)},
			},
		},
		{
			name: "a pool the server no longer reports is removed from state",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
					)},
			},
			wantRemoved: true,
		},
		{
			name: "a cleared record is removed from state",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "none", "active", "null"),
					)},
			},
			wantRemoved: true,
		},
		{
			name: "a permission failure keeps state and names the call",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")},
			},
			wantSummary: "Error reading pool decommission",
			wantDetail:  "Could not read the decommission of pool " + fmt.Sprintf("%q", decommissionPoolB),
		},
		{
			name: "a single-pool 501 keeps state and explains the topology",
			pool: "/data",
			responses: []scriptedResponse{
				{status: http.StatusNotImplemented,
					body: `<Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented</Message></Error>`},
				{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
				{method: http.MethodGet, pathContains: "pools/list", status: http.StatusOK,
					body: `[{"id":0,"cmdline":"/data","status":"active"}]`},
			},
			wantSummary: "Error reading pool decommission",
			wantDetail:  "only on a deployment with at least two storage pools",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, server := scriptedClient(t, tc.responses...)
			h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

			resp := h.read(poolDecommissionState(tc.pool))

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				server.assertAllConsumed(t)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("status"), "running")
				assertStateString(t, resp.State, path.Root("object"), "config/config.json")
				server.assertAllConsumed(t)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
			server.assertAllConsumed(t)
		})
	}

	t.Run("update copies the plan without calling the server", func(t *testing.T) {
		client, server := scriptedClient(t)
		h := newCRUDHarness(t, NewPoolDecommissionResource(), client)
		plan := poolDecommissionPlan(decommissionPoolB)
		plan.Status = types.StringValue("running")

		resp := h.update(plan, poolDecommissionState(decommissionPoolB))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("status"), "running")
		server.assertAllConsumed(t)
	})

	deleteCases := []struct {
		name        string
		pool        string
		responses   []scriptedResponse
		wantSummary string
		wantDetail  string
	}{
		{
			name: "an absent pool needs no mutation",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(0, decommissionPoolA, "none", "active", "null"),
					)},
			},
		},
		{
			name: "a completed drain is terminal and is left alone",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "complete", "decommissioned", decommissionCompleteInfo),
					)},
			},
		},
		{
			name: "a running drain is canceled and its record cleared",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
					)},
				{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB, status: http.StatusOK},
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "canceled", "blocked", decommissionCanceledInfo),
					)},
				{method: http.MethodPost, pathContains: "pools/clear", queryPool: decommissionPoolB, status: http.StatusOK},
			},
		},
		{
			name: "a refused cancel is surfaced, not swallowed",
			pool: decommissionPoolB,
			responses: []scriptedResponse{
				{method: http.MethodGet, pathContains: "decommission/status", status: http.StatusOK,
					body: decommissionStatusBody(
						decommissionPoolBody(1, decommissionPoolB, "running", "decommissioning", decommissionRunningInfo),
					)},
				{method: http.MethodPost, pathContains: "pools/cancel", queryPool: decommissionPoolB,
					status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "boom")},
			},
			wantSummary: "Error canceling pool decommission",
			wantDetail:  "Could not cancel the decommission of pool " + fmt.Sprintf("%q", decommissionPoolB),
		},
		{
			name: "a single-pool 501 is surfaced with the topology explanation",
			pool: "/data",
			responses: []scriptedResponse{
				{status: http.StatusNotImplemented,
					body: `<Error><Code>NotImplemented</Code><Message>A header you provided implies functionality that is not implemented</Message></Error>`},
				{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
				{method: http.MethodGet, pathContains: "pools/list", status: http.StatusOK,
					body: `[{"id":0,"cmdline":"/data","status":"active"}]`},
			},
			wantSummary: "Error canceling pool decommission",
			wantDetail:  "only on a deployment with at least two storage pools",
		},
	}

	for _, tc := range deleteCases {
		t.Run("destroy: "+tc.name, func(t *testing.T) {
			client, server := scriptedClient(t, tc.responses...)
			h := newCRUDHarness(t, NewPoolDecommissionResource(), client)

			resp := h.delete(poolDecommissionState(tc.pool))

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
			} else {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			}
			server.assertAllConsumed(t)
		})
	}
}

func TestPoolDecommissionErrorHelpers(t *testing.T) {
	r := NewPoolDecommissionResource().(*PoolDecommissionResource)

	if !poolDecommissionUnsupported(&rustfs.APIError{StatusCode: http.StatusNotImplemented}) {
		t.Error("a raw 501 APIError must count as unsupported")
	}
	if poolDecommissionUnsupported(&rustfs.APIError{StatusCode: http.StatusForbidden}) {
		t.Error("a 403 must not count as unsupported")
	}
	if got := quotedNames(nil); got != "no pools" {
		t.Errorf("quotedNames(nil) = %q", got)
	}

	var diags diag.Diagnostics
	r.addDecommissionError(context.Background(), &diags, "Error", "read", decommissionPoolB, nil)
	assertErrorDiagnostic(t, diags, "Error", "the server returned no error detail")
}

func TestPoolDecommissionImportState(t *testing.T) {
	r := NewPoolDecommissionResource()
	importable := r.(resource.ResourceWithImportState)
	sch := resourceSchemaOf(t, r)

	resp := &resource.ImportStateResponse{State: resourceState(t, sch, poolDecommissionPlan(""))}
	importable.ImportState(context.Background(), resource.ImportStateRequest{ID: decommissionPoolB}, resp)

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("pool"), decommissionPoolB)
	assertStateString(t, resp.State, path.Root("id"), decommissionPoolB)
}

// TestAccPoolDecommission_lifecycle drives a real drain. It is opt-in because
// it retires one pool irreversibly: set RUSTFS_DECOMMISSION_TEST_POOL to the
// cmdline of a disposable pool on a deployment with at least two pools.
func TestAccPoolDecommission_lifecycle(t *testing.T) {
	testAccPreCheck(t)

	pool := os.Getenv("RUSTFS_DECOMMISSION_TEST_POOL")
	if pool == "" {
		t.Skip("set RUSTFS_DECOMMISSION_TEST_POOL to the cmdline of a disposable pool on a multi-pool deployment")
	}

	client := testAccPoolDecommissionClient(t)
	statuses, err := client.DecommissionStatuses(context.Background())
	if err != nil {
		if poolDecommissionUnsupported(err) {
			t.Skipf("decommission is not served on this deployment: %v", err)
		}
		t.Fatalf("reading decommission status: %v", err)
	}
	if len(statuses) < 2 {
		t.Skipf("the deployment reports %d pools; decommission needs at least two", len(statuses))
	}
	current := findPoolDecommission(statuses, pool)
	if current == nil {
		t.Fatalf("pool %q is not reported by the deployment", pool)
	}
	if current.Status != "none" {
		t.Skipf("pool %q already has a %s decommission record", pool, current.Status)
	}

	resourceName := "rustfs_pool_decommission.test"

	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			return testAccCheckPoolDecommissionDestroy(t, s)
		},
		Steps: []tfresource.TestStep{
			{
				Config: testAccPoolDecommissionConfig(pool),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "id", pool),
					tfresource.TestCheckResourceAttr(resourceName, "pool", pool),
					tfresource.TestCheckResourceAttrSet(resourceName, "status"),
					testAccCheckPoolDecommissionStarted(t, resourceName),
				),
			},
			{
				// Import by pool cmdline. Drain progress moves between steps, so
				// only the stable identity attributes are verified.
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        pool,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "id",
				ImportStateVerifyIgnore: []string{
					"status", "pool_status", "started_at", "complete", "failed", "canceled", "queued",
					"queued_buckets", "decommissioned_buckets", "bucket", "prefix", "object", "stage",
					"waiting_reason", "start_size", "total_size", "current_size",
					"objects_decommissioned", "objects_decommissioned_failed",
					"bytes_decommissioned", "bytes_decommissioned_failed",
				},
			},
			{
				// Progress is computed state, so the post-apply plan is empty.
				Config:   testAccPoolDecommissionConfig(pool),
				PlanOnly: true,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func testAccPoolDecommissionConfig(pool string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_pool_decommission" "test" {
  pool    = %q
  confirm = %q
}
`, pool, pool)
}

func testAccPoolDecommissionClient(t *testing.T) rustfs.RustfsAdmin {
	t.Helper()
	return rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}

func testAccCheckPoolDecommissionStarted(t *testing.T, n string) tfresource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		pool := rs.Primary.Attributes["pool"]
		client := testAccPoolDecommissionClient(t)
		statuses, err := client.DecommissionStatuses(context.Background())
		if err != nil {
			return fmt.Errorf("reading decommission status: %w", err)
		}
		current := findPoolDecommission(statuses, pool)
		if current == nil {
			return fmt.Errorf("pool %q is no longer reported", pool)
		}
		if current.Status == "none" {
			return fmt.Errorf("pool %q has no decommission record", pool)
		}
		return nil
	}
}

// testAccCheckPoolDecommissionDestroy accepts the two states destroy can leave:
// no record at all (an in-flight drain was canceled and cleared), or a
// completed record, which RustFS refuses to cancel or clear.
func testAccCheckPoolDecommissionDestroy(t *testing.T, s *terraform.State) error {
	client := testAccPoolDecommissionClient(t)
	statuses, err := client.DecommissionStatuses(context.Background())
	if err != nil {
		return fmt.Errorf("reading decommission status: %w", err)
	}
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_pool_decommission" {
			continue
		}
		pool := rs.Primary.Attributes["pool"]
		current := findPoolDecommission(statuses, pool)
		if current == nil || current.Status == "none" || current.Status == "complete" {
			continue
		}
		return fmt.Errorf("pool %q is still %s after destroy", pool, current.Status)
	}
	return nil
}
