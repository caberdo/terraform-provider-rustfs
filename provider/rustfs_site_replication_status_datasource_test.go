// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func TestSiteReplicationStatusDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewSiteReplicationStatusDataSource().Metadata(
		context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_site_replication_status" {
		t.Errorf("type name = %q, want rustfs_site_replication_status", resp.TypeName)
	}
}

func TestSiteReplicationStatusDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewSiteReplicationStatusDataSource())

	for _, name := range []string{
		"id", "enabled", "name", "deployment_id", "api_version",
		"max_buckets", "max_users", "max_groups", "max_policies", "max_ilm_expiry_rules",
		"status_available", "sites", "peer_errors",
		"retry_pending", "retry_failed", "retry_last_error", "metainfo",
	} {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if attr.GetDescription() == "" {
			t.Errorf("%s has no description, tfplugindocs would publish an empty row", name)
		}
		if !attr.IsComputed() {
			t.Errorf("%s must be Computed: the data source has no inputs", name)
		}
	}

	sites, ok := sch.Attributes["sites"].(datasourceschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("sites is %T, want schema.ListNestedAttribute", sch.Attributes["sites"])
	}
	for _, name := range []string{
		"name", "endpoint", "deployment_id", "sync_state", "skip_tls_verify",
		"replicate_ilm_expiry", "stats",
	} {
		if _, ok := sites.NestedObject.Attributes[name]; !ok {
			t.Errorf("missing nested attribute %q on sites", name)
		}
	}

	stats, ok := sites.NestedObject.Attributes["stats"].(datasourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("sites.stats is %T, want schema.SingleNestedAttribute", sites.NestedObject.Attributes["stats"])
	}
	for name := range siteReplicationStatsAttrTypes {
		if _, ok := stats.Attributes[name]; !ok {
			t.Errorf("missing nested attribute %q on sites.stats", name)
		}
	}

	meta, ok := sch.Attributes["metainfo"].(datasourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("metainfo is %T, want schema.SingleNestedAttribute", sch.Attributes["metainfo"])
	}
	for name := range siteReplicationMetaInfoAttrTypes {
		if _, ok := meta.Attributes[name]; !ok {
			t.Errorf("missing nested attribute %q on metainfo", name)
		}
	}
}

func siteReplicationStatusConfig() *SiteReplicationStatusDataSourceModel {
	return &SiteReplicationStatusDataSourceModel{
		ID:                types.StringNull(),
		Enabled:           types.BoolNull(),
		Name:              types.StringNull(),
		DeploymentID:      types.StringNull(),
		APIVersion:        types.StringNull(),
		MaxBuckets:        types.Int64Null(),
		MaxUsers:          types.Int64Null(),
		MaxGroups:         types.Int64Null(),
		MaxPolicies:       types.Int64Null(),
		MaxILMExpiryRules: types.Int64Null(),
		StatusAvailable:   types.BoolNull(),
		Sites:             types.ListNull(types.ObjectType{AttrTypes: siteReplicationSiteAttrTypes}),
		PeerErrors:        types.ListNull(types.ObjectType{AttrTypes: siteReplicationPeerErrorAttrTypes}),
		RetryPending:      types.Int64Null(),
		RetryFailed:       types.Int64Null(),
		RetryLastError:    types.StringNull(),
		MetaInfo:          types.ObjectNull(siteReplicationMetaInfoAttrTypes),
	}
}

func readSiteReplicationStatus(
	t *testing.T,
	d *SiteReplicationStatusDataSource,
	sch datasourceschema.Schema,
	model *SiteReplicationStatusDataSourceModel,
) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func siteReplicationStatusDataSource(t *testing.T, routes ...requestRoute) *SiteReplicationStatusDataSource {
	t.Helper()

	client, _ := routedClient(t, routes...)
	d := NewSiteReplicationStatusDataSource().(*SiteReplicationStatusDataSource)
	d.client = client
	return d
}

func siteReplicationStatusSites(t *testing.T, state tfsdk.State) []siteReplicationSiteModel {
	t.Helper()

	var sites []siteReplicationSiteModel
	if diags := state.GetAttribute(context.Background(), path.Root("sites"), &sites); diags.HasError() {
		t.Fatalf("sites attribute: %v", diags)
	}
	return sites
}

func siteReplicationStatusSiteStats(t *testing.T, obj types.Object) siteReplicationStatsModel {
	t.Helper()

	var stats siteReplicationStatsModel
	if diags := obj.As(context.Background(), &stats, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("stats object: %v", diags)
	}
	return stats
}

func TestSiteReplicationStatusDataSourceReadNoSites(t *testing.T) {
	d := siteReplicationStatusDataSource(t,
		requestRoute{pathContains: "site-replication/info", status: http.StatusOK,
			body: `{"enabled":true,"name":"alpha","sites":[],"apiVersion":"1"}`},
		requestRoute{pathContains: "site-replication/status", status: http.StatusOK,
			body: `{"enabled":true,"Sites":{},"StatsSummary":{},"PeerErrors":{},"apiVersion":"1"}`},
		requestRoute{pathContains: "site-replication/metainfo", status: http.StatusOK,
			body: `{"enabled":true,"name":"alpha","deploymentID":"dep-a","state":{"name":"alpha"}}`},
	)

	resp := readSiteReplicationStatus(t, d, dataSourceSchemaOf(t, d), siteReplicationStatusConfig())
	requireNoDiagnostics(t, resp.Diagnostics)

	assertStateString(t, resp.State, path.Root("id"), "site-replication-status")
	assertStateString(t, resp.State, path.Root("name"), "alpha")
	assertStateString(t, resp.State, path.Root("deployment_id"), "dep-a")
	assertStateString(t, resp.State, path.Root("api_version"), "1")
	assertStateBool(t, resp.State, path.Root("enabled"), true)
	assertStateBool(t, resp.State, path.Root("status_available"), true)

	if sites := siteReplicationStatusSites(t, resp.State); len(sites) != 0 {
		t.Errorf("sites = %d, want 0", len(sites))
	}

	var retry types.Int64
	if diags := resp.State.GetAttribute(context.Background(), path.Root("retry_failed"), &retry); diags.HasError() {
		t.Fatalf("retry_failed: %v", diags)
	}
	if retry.ValueInt64() != 0 {
		t.Errorf("retry_failed = %d, want 0", retry.ValueInt64())
	}
}

func TestSiteReplicationStatusDataSourceReadSites(t *testing.T) {
	d := siteReplicationStatusDataSource(t,
		// info lists beta before alpha on purpose: the data source sorts.
		requestRoute{pathContains: "site-replication/info", status: http.StatusOK, body: `{"enabled":true,` +
			`"name":"alpha","apiVersion":"1","sites":[` +
			`{"name":"beta","endpoint":"http://beta:9000","deploymentID":"dep-b","skipTlsVerify":false},` +
			`{"name":"alpha","endpoint":"http://alpha:9000","deploymentID":"dep-a","skipTlsVerify":false},` +
			`{"name":"gamma","endpoint":"http://gamma:9000","deploymentID":"dep-c","skipTlsVerify":true}]}`},
		requestRoute{pathContains: "site-replication/status", status: http.StatusOK, body: `{"enabled":true,` +
			`"MaxBuckets":9,"MaxUsers":4,"MaxGroups":3,"MaxPolicies":2,"MaxILMExpiryRules":1,"apiVersion":"1",` +
			`"Sites":{` +
			`"dep-a":{"name":"alpha","endpoint":"http://alpha:9000","deploymentID":"dep-a",` +
			`"sync":"enable","skipTlsVerify":true,"replicate-ilm-expiry":true},` +
			`"dep-b":{"name":"beta","endpoint":"http://beta:9000","deploymentID":"dep-b",` +
			`"sync":"disable","skipTlsVerify":false,"replicate-ilm-expiry":false}},` +
			`"StatsSummary":{` +
			`"dep-a":{"ReplicatedBuckets":7,"ReplicatedTags":2,"TotalBucketsCount":9,"TotalCorsConfigCount":1},` +
			`"dep-b":{"ReplicatedBuckets":0,"TotalBucketsCount":9}},` +
			`"PeerErrors":{` +
			`"dep-z":{"name":"zeta","endpoint":"http://zeta:9000","error":"timeout"},` +
			`"dep-y":{"name":"eta","endpoint":"http://eta:9000","error":"refused"}},` +
			`"RetryStats":{"pending":3,"failed":2,"lastError":"dial tcp: refused"}}`},
		requestRoute{pathContains: "site-replication/metainfo", status: http.StatusOK, body: `{"enabled":true,` +
			`"name":"alpha","deploymentID":"dep-a","apiVersion":"1",` +
			`"buckets":{"photos":{}},"policies":{"readonly":{}},"userPolicies":{"alice":{}},` +
			`"userInfoMap":{"alice":{},"bob":{}},"groupDescMap":{"auditors":{}},` +
			`"groupPolicies":{"auditors":{}},"replicationCfg":{"photos":{}},` +
			`"ilmExpiryRules":{"rule-1":{}},"state":{"name":"alpha","peers":{"dep-a":{},"dep-b":{}},` +
			`"updatedAt":"2026-01-02T03:04:05Z"}}`},
	)

	resp := readSiteReplicationStatus(t, d, dataSourceSchemaOf(t, d), siteReplicationStatusConfig())
	requireNoDiagnostics(t, resp.Diagnostics)

	assertStateBool(t, resp.State, path.Root("enabled"), true)
	assertStateBool(t, resp.State, path.Root("status_available"), true)
	assertStateString(t, resp.State, path.Root("deployment_id"), "dep-a")

	sites := siteReplicationStatusSites(t, resp.State)
	if len(sites) != 3 {
		t.Fatalf("sites = %d, want 3", len(sites))
	}

	names := []string{sites[0].Name.ValueString(), sites[1].Name.ValueString(), sites[2].Name.ValueString()}
	if names[0] != "alpha" || names[1] != "beta" || names[2] != "gamma" {
		t.Errorf("site order = %v, want [alpha beta gamma]", names)
	}

	alpha := sites[0]
	if alpha.SyncState.ValueString() != "enable" {
		t.Errorf("alpha sync_state = %q, want enable", alpha.SyncState.ValueString())
	}
	if !alpha.SkipTLSVerify.ValueBool() {
		t.Error("alpha skip_tls_verify = false, want the status value true")
	}
	if !alpha.ReplicateILMExpiry.ValueBool() {
		t.Error("alpha replicate_ilm_expiry = false, want true")
	}
	if alpha.Endpoint.ValueString() != "http://alpha:9000" {
		t.Errorf("alpha endpoint = %q", alpha.Endpoint.ValueString())
	}

	alphaStats := siteReplicationStatusSiteStats(t, alpha.Stats)
	if alphaStats.ReplicatedBuckets.ValueInt64() != 7 {
		t.Errorf("alpha replicated_buckets = %d, want 7", alphaStats.ReplicatedBuckets.ValueInt64())
	}
	if alphaStats.TotalBucketsCount.ValueInt64() != 9 {
		t.Errorf("alpha total_buckets_count = %d, want 9", alphaStats.TotalBucketsCount.ValueInt64())
	}
	if alphaStats.TotalCorsConfigCount.ValueInt64() != 1 {
		t.Errorf("alpha total_cors_config_count = %d, want 1", alphaStats.TotalCorsConfigCount.ValueInt64())
	}

	if got := sites[1].SyncState.ValueString(); got != "disable" {
		t.Errorf("beta sync_state = %q, want disable", got)
	}
	// gamma is configured but absent from the status answer.
	if got := sites[2].SyncState.ValueString(); got != "unknown" {
		t.Errorf("gamma sync_state = %q, want unknown", got)
	}
	if !sites[2].SkipTLSVerify.ValueBool() {
		t.Error("gamma skip_tls_verify = false, want the info value true")
	}
	gammaStats := siteReplicationStatusSiteStats(t, sites[2].Stats)
	if gammaStats.ReplicatedBuckets.ValueInt64() != 0 {
		t.Errorf("gamma replicated_buckets = %d, want 0 for a site with no status", gammaStats.ReplicatedBuckets.ValueInt64())
	}

	var peerErrors []siteReplicationPeerErrorModel
	if diags := resp.State.GetAttribute(context.Background(), path.Root("peer_errors"), &peerErrors); diags.HasError() {
		t.Fatalf("peer_errors: %v", diags)
	}
	if len(peerErrors) != 2 {
		t.Fatalf("peer_errors = %d, want 2", len(peerErrors))
	}
	if peerErrors[0].Name.ValueString() != "eta" || peerErrors[1].Name.ValueString() != "zeta" {
		t.Errorf("peer error order = [%s %s], want [eta zeta]",
			peerErrors[0].Name.ValueString(), peerErrors[1].Name.ValueString())
	}
	if peerErrors[0].Error.ValueString() != "refused" {
		t.Errorf("peer error detail = %q, want refused", peerErrors[0].Error.ValueString())
	}

	assertStateString(t, resp.State, path.Root("retry_last_error"), "dial tcp: refused")

	var retryPending, retryFailed types.Int64
	if diags := resp.State.GetAttribute(context.Background(), path.Root("retry_pending"), &retryPending); diags.HasError() {
		t.Fatalf("retry_pending: %v", diags)
	}
	if diags := resp.State.GetAttribute(context.Background(), path.Root("retry_failed"), &retryFailed); diags.HasError() {
		t.Fatalf("retry_failed: %v", diags)
	}
	if retryPending.ValueInt64() != 3 || retryFailed.ValueInt64() != 2 {
		t.Errorf("retry counters = (%d, %d), want (3, 2)",
			retryPending.ValueInt64(), retryFailed.ValueInt64())
	}

	var meta siteReplicationMetaInfoModel
	var metaObj types.Object
	if diags := resp.State.GetAttribute(context.Background(), path.Root("metainfo"), &metaObj); diags.HasError() {
		t.Fatalf("metainfo attribute: %v", diags)
	}
	if diags := metaObj.As(context.Background(), &meta, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("metainfo object: %v", diags)
	}
	if meta.DeploymentID.ValueString() != "dep-a" {
		t.Errorf("metainfo deployment_id = %q, want dep-a", meta.DeploymentID.ValueString())
	}
	if meta.PeerCount.ValueInt64() != 2 || meta.BucketCount.ValueInt64() != 1 ||
		meta.UserCount.ValueInt64() != 2 || meta.ILMExpiryRuleCount.ValueInt64() != 1 {
		t.Errorf("metainfo counts = %+v", meta)
	}
	if meta.StateUpdatedAt.ValueString() != "2026-01-02T03:04:05Z" {
		t.Errorf("metainfo state_updated_at = %q", meta.StateUpdatedAt.ValueString())
	}
}

func TestSiteReplicationStatusDataSourceStatusFallback(t *testing.T) {
	d := siteReplicationStatusDataSource(t,
		requestRoute{pathContains: "site-replication/info", status: http.StatusOK, body: `{"enabled":true,` +
			`"name":"alpha","apiVersion":"1","sites":[` +
			`{"name":"alpha","endpoint":"http://alpha:9000","deploymentID":"dep-a"}]}`},
		requestRoute{pathContains: "site-replication/status", status: http.StatusNotImplemented,
			body: s3ErrorXML("NotImplemented", "not implemented")},
		requestRoute{pathContains: "site-replication/metainfo", status: http.StatusOK,
			body: `{"enabled":true,"name":"alpha","deploymentID":"dep-a","state":{"name":"alpha"}}`},
		requestRoute{pathContains: "healthinfo", status: http.StatusOK,
			body: `{"version":"refs/tags/1.0.1"}`},
	)

	resp := readSiteReplicationStatus(t, d, dataSourceSchemaOf(t, d), siteReplicationStatusConfig())
	requireNoDiagnostics(t, resp.Diagnostics)

	assertStateBool(t, resp.State, path.Root("status_available"), false)
	assertStateBool(t, resp.State, path.Root("enabled"), true)
	assertStateString(t, resp.State, path.Root("deployment_id"), "dep-a")

	sites := siteReplicationStatusSites(t, resp.State)
	if len(sites) != 1 {
		t.Fatalf("sites = %d, want the info site", len(sites))
	}
	if got := sites[0].SyncState.ValueString(); got != "unknown" {
		t.Errorf("sync_state = %q, want unknown without a status route", got)
	}
	stats := siteReplicationStatusSiteStats(t, sites[0].Stats)
	if stats.ReplicatedBuckets.ValueInt64() != 0 {
		t.Errorf("replicated_buckets = %d, want 0", stats.ReplicatedBuckets.ValueInt64())
	}

	var peerErrors []siteReplicationPeerErrorModel
	if diags := resp.State.GetAttribute(context.Background(), path.Root("peer_errors"), &peerErrors); diags.HasError() {
		t.Fatalf("peer_errors: %v", diags)
	}
	if len(peerErrors) != 0 {
		t.Errorf("peer_errors = %d, want 0 without a status route", len(peerErrors))
	}
}

func TestSiteReplicationStatusDataSourceStatusError(t *testing.T) {
	d := siteReplicationStatusDataSource(t,
		requestRoute{pathContains: "site-replication/info", status: http.StatusOK,
			body: `{"enabled":true,"name":"alpha"}`},
		requestRoute{pathContains: "site-replication/status", status: http.StatusForbidden,
			body: s3ErrorXML("AccessDenied", "Access Denied.")},
		requestRoute{pathContains: "site-replication/metainfo", status: http.StatusOK,
			body: `{"enabled":true,"name":"alpha"}`},
	)

	resp := readSiteReplicationStatus(t, d, dataSourceSchemaOf(t, d), siteReplicationStatusConfig())
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading site replication status", "AccessDenied")
}

func TestSiteReplicationStatusDataSourceMetaInfoError(t *testing.T) {
	d := siteReplicationStatusDataSource(t,
		requestRoute{pathContains: "site-replication/info", status: http.StatusOK,
			body: `{"enabled":true,"name":"alpha"}`},
		requestRoute{pathContains: "site-replication/status", status: http.StatusOK,
			body: `{"enabled":true}`},
		requestRoute{pathContains: "site-replication/metainfo", status: http.StatusForbidden,
			body: s3ErrorXML("AccessDenied", "Access Denied.")},
	)

	resp := readSiteReplicationStatus(t, d, dataSourceSchemaOf(t, d), siteReplicationStatusConfig())
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading site replication metainfo", "AccessDenied")
}

func TestSiteReplicationStatusDataSourceInfoError(t *testing.T) {
	d := siteReplicationStatusDataSource(t,
		requestRoute{pathContains: "site-replication/info", status: http.StatusForbidden,
			body: s3ErrorXML("AccessDenied", "Access Denied.")},
	)

	resp := readSiteReplicationStatus(t, d, dataSourceSchemaOf(t, d), siteReplicationStatusConfig())
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading site replication info", "AccessDenied")
}
