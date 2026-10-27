// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSiteReplicationResyncResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewSiteReplicationResyncResource().Metadata(
		context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_site_replication_resync" {
		t.Errorf("type name = %q, want rustfs_site_replication_resync", resp.TypeName)
	}
}

func hasRequiresReplaceObject(modifiers []planmodifier.Object) bool {
	want := fmt.Sprintf("%T", objectplanmodifier.RequiresReplace())
	for _, modifier := range modifiers {
		if fmt.Sprintf("%T", modifier) == want {
			return true
		}
	}
	return false
}

func hasRequiresReplaceMap(modifiers []planmodifier.Map) bool {
	want := fmt.Sprintf("%T", mapplanmodifier.RequiresReplace())
	for _, modifier := range modifiers {
		if fmt.Sprintf("%T", modifier) == want {
			return true
		}
	}
	return false
}

func TestSiteReplicationResyncResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewSiteReplicationResyncResource())

	for _, name := range []string{
		"id", "operation", "peer", "triggers",
		"resync_id", "op_type", "status", "state", "message",
	} {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if attr.GetDescription() == "" {
			t.Errorf("%s has no description, tfplugindocs would publish an empty row", name)
		}
	}

	operation, ok := sch.Attributes["operation"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("operation is %T, want schema.StringAttribute", sch.Attributes["operation"])
	}
	if !operation.Required || operation.Optional || operation.Computed {
		t.Errorf("operation must be Required only: %+v", operation)
	}
	if !hasRequiresReplace(operation.PlanModifiers) {
		t.Error("operation must carry RequiresReplace: changing it starts a new operation")
	}

	peer, ok := sch.Attributes["peer"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("peer is %T, want schema.SingleNestedAttribute", sch.Attributes["peer"])
	}
	if !peer.Required || peer.Optional || peer.Computed {
		t.Errorf("peer must be Required only: %+v", peer)
	}
	if !hasRequiresReplaceObject(peer.PlanModifiers) {
		t.Error("peer block must carry RequiresReplace")
	}
	for _, name := range []string{"name", "endpoint"} {
		attr, ok := peer.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("peer.%s is %T, want schema.StringAttribute", name, peer.Attributes[name])
		}
		if !attr.Optional || attr.Required || attr.Computed {
			t.Errorf("peer.%s must be Optional only: %+v", name, attr)
		}
		if !hasRequiresReplace(attr.PlanModifiers) {
			t.Errorf("peer.%s must carry RequiresReplace", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("peer.%s has no description", name)
		}
	}

	deploymentID, ok := peer.Attributes["deployment_id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("peer.deployment_id is %T, want schema.StringAttribute", peer.Attributes["deployment_id"])
	}
	if !deploymentID.Required || deploymentID.Optional || deploymentID.Computed {
		t.Errorf("peer.deployment_id must be Required only: %+v", deploymentID)
	}
	if !hasRequiresReplace(deploymentID.PlanModifiers) {
		t.Error("peer.deployment_id must carry RequiresReplace")
	}
	if deploymentID.GetDescription() == "" {
		t.Error("peer.deployment_id has no description")
	}

	triggers, ok := sch.Attributes["triggers"].(schema.MapAttribute)
	if !ok {
		t.Fatalf("triggers is %T, want schema.MapAttribute", sch.Attributes["triggers"])
	}
	if !triggers.Optional || triggers.Required || triggers.Computed {
		t.Errorf("triggers must be Optional only: %+v", triggers)
	}
	if !hasRequiresReplaceMap(triggers.PlanModifiers) {
		t.Error("triggers must carry RequiresReplace")
	}

	for _, name := range []string{"id", "resync_id", "op_type", "status", "state", "message"} {
		attr, ok := sch.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s is %T, want schema.StringAttribute", name, sch.Attributes[name])
		}
		if !attr.Computed || attr.Required || attr.Optional {
			t.Errorf("%s must be Computed only: %+v", name, attr)
		}
	}
}

func siteReplicationResyncPeerObject(t *testing.T, name, endpoint, deploymentID string) types.Object {
	t.Helper()

	peer, diags := types.ObjectValue(siteReplicationResyncPeerAttrTypes, map[string]attr.Value{
		"name":          types.StringValue(name),
		"endpoint":      types.StringValue(endpoint),
		"deployment_id": types.StringValue(deploymentID),
	})
	if diags.HasError() {
		t.Fatalf("building the peer object: %v", diags)
	}
	return peer
}

func siteReplicationResyncPlan(t *testing.T, operation, deploymentID string) *SiteReplicationResyncResourceModel {
	t.Helper()

	return &SiteReplicationResyncResourceModel{
		ID:        types.StringUnknown(),
		Operation: types.StringValue(operation),
		Peer:      siteReplicationResyncPeerObject(t, "beta", "http://beta:9000", deploymentID),
		Triggers:  types.MapNull(types.StringType),
		ResyncID:  types.StringUnknown(),
		OpType:    types.StringUnknown(),
		Status:    types.StringUnknown(),
		State:     types.StringUnknown(),
		Message:   types.StringUnknown(),
	}
}

func siteReplicationResyncState(t *testing.T) *SiteReplicationResyncResourceModel {
	t.Helper()

	return &SiteReplicationResyncResourceModel{
		ID:        types.StringValue("op-1"),
		Operation: types.StringValue("start"),
		Peer:      siteReplicationResyncPeerObject(t, "beta", "http://beta:9000", "dep-b"),
		Triggers:  types.MapNull(types.StringType),
		ResyncID:  types.StringValue("op-1"),
		OpType:    types.StringValue("start"),
		Status:    types.StringValue("success"),
		State:     types.StringValue("active"),
		Message:   types.StringValue(""),
	}
}

func TestSiteReplicationResyncResourceLifecyclePaths(t *testing.T) {
	t.Run("create success records the server answer", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			pathContains: "site-replication/resync/op",
			status:       http.StatusOK,
			body:         `{"op":"start","id":"op-1","status":"success","state":"active"}`,
		})
		h := newCRUDHarness(t, NewSiteReplicationResyncResource(), client)

		resp := h.create(siteReplicationResyncPlan(t, "start", "dep-b"))
		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "PUT /rustfs/admin/v3/site-replication/resync/op")

		assertStateString(t, resp.State, path.Root("id"), "op-1")
		assertStateString(t, resp.State, path.Root("resync_id"), "op-1")
		assertStateString(t, resp.State, path.Root("op_type"), "start")
		assertStateString(t, resp.State, path.Root("status"), "success")
		assertStateString(t, resp.State, path.Root("state"), "active")
		assertStateString(t, resp.State, path.Root("message"), "")
	})

	t.Run("create without a resync id falls back to operation and deployment", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			pathContains: "site-replication/resync/op",
			status:       http.StatusOK,
			body:         `{"op":"cancel","status":"success","errorDetail":"nothing to cancel"}`,
		})
		h := newCRUDHarness(t, NewSiteReplicationResyncResource(), client)

		resp := h.create(siteReplicationResyncPlan(t, "cancel", "dep-b"))
		requireNoDiagnostics(t, resp.Diagnostics)

		assertStateString(t, resp.State, path.Root("id"), "cancel/dep-b")
		assertStateString(t, resp.State, path.Root("resync_id"), "")
		assertStateString(t, resp.State, path.Root("message"), "nothing to cancel")
	})

	t.Run("failed create writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			pathContains: "site-replication/resync/op",
			status:       http.StatusBadRequest,
			body:         s3ErrorXML("InvalidRequest", "invalid deployment id"),
		})
		h := newCRUDHarness(t, NewSiteReplicationResyncResource(), client)

		plan := siteReplicationResyncPlan(t, "start", "dep-b")
		resp := h.create(plan)
		assertErrorDiagnostic(t, resp.Diagnostics,
			"Error running site replication resync", "invalid deployment id")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("unsupported route explains the capability gap", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{pathContains: "site-replication/resync/op", status: http.StatusNotImplemented,
				body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{pathContains: "healthinfo", status: http.StatusOK,
				body: `{"version":"refs/tags/1.0.1"}`},
		)
		h := newCRUDHarness(t, NewSiteReplicationResyncResource(), client)

		plan := siteReplicationResyncPlan(t, "status", "dep-b")
		resp := h.create(plan)
		assertErrorDiagnostic(t, resp.Diagnostics,
			"Site replication resync is not available on this RustFS server", "RustFS 1.0.1")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("read echoes the recorded operation without a request", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewSiteReplicationResyncResource(), client)

		resp := h.read(siteReplicationResyncState(t))
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "op-1")
		assertStateString(t, resp.State, path.Root("status"), "success")
		if requests := rec.snapshot(); len(requests) != 0 {
			t.Fatalf("read must not call the server, saw %v", requests)
		}
	})

	t.Run("delete is a no-op", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewSiteReplicationResyncResource(), client)

		resp := h.delete(siteReplicationResyncState(t))
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		if requests := rec.snapshot(); len(requests) != 0 {
			t.Fatalf("delete must not call the server, saw %v", requests)
		}
	})
}
