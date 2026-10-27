// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSiteReplicationAdd(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = readRequestBody(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	site := SiteReplicationSite{
		Name:          "peer",
		Endpoint:      "http://localhost:9002",
		AccessKey:     "peeradmin",
		SecretKey:     "peersecret",
		SkipTLSVerify: false,
	}
	if err := c.SiteReplicationAdd(context.Background(), site); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/add") {
		t.Errorf("wrong path: %s", gotPath)
	}
	// The body is never printed on failure: it carries the peer credentials.
	if !strings.Contains(gotBody, `"endpoints":"http://localhost:9002"`) {
		t.Error("add body must carry the endpoint in its \"endpoints\" field")
	}
	if !strings.Contains(gotBody, `"accessKey":"peeradmin"`) {
		t.Error("add body must carry the peer access key (value redacted)")
	}
	var payload []SiteReplicationSite
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("add body must be a JSON array: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("add body carries %d sites, want one", len(payload))
	}
	if payload[0].Name != "peer" {
		t.Errorf("add body name = %q, want peer", payload[0].Name)
	}
}

func TestEditPeer(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = readRequestBody(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	site := SiteReplicationPeerEdit{
		Name:          "peer",
		Endpoint:      "http://localhost:9003",
		SkipTLSVerify: true,
	}
	if err := c.EditPeer(context.Background(), site); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/edit") {
		t.Errorf("wrong path: %s", gotPath)
	}
	// site-replication/edit parses a single PeerInfo whose endpoint field is
	// "endpoint" (singular); the add-only "endpoints" field and the array body
	// are rejected by RustFS 1.0.3.
	// The body is never printed on failure: it carries the peer credentials.
	if strings.Contains(gotBody, `"endpoints"`) {
		t.Error("edit body must not use the add-only \"endpoints\" field")
	}
	if !strings.HasPrefix(gotBody, "{") {
		t.Error("edit body must be a single object, not the add-only array")
	}
	var payload SiteReplicationPeerEdit
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("edit body must be a PeerInfo object: %v", err)
	}
	if payload.Name != "peer" || payload.Endpoint != "http://localhost:9003" {
		t.Errorf("edit body name/endpoint = %q/%q, want peer/http://localhost:9003", payload.Name, payload.Endpoint)
	}
	if !payload.SkipTLSVerify {
		t.Error("edit body must set skipTlsVerify")
	}
}

func TestRemovePeer(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = readRequestBody(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	if err := c.RemovePeer(context.Background(), []string{"peer"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/remove") {
		t.Errorf("wrong path: %s", gotPath)
	}
	var body struct {
		Sites []string `json:"sites"`
	}
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if len(body.Sites) != 1 || body.Sites[0] != "peer" {
		t.Errorf("remove body carries %d sites, want one", len(body.Sites))
	}
}

func TestSiteReplicationInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/site-replication/info") {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":true,"name":"local","sites":[{"name":"peer","endpoint":"http://localhost:9002","deploymentID":"dep1"}]}`))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	info, err := c.SiteReplicationInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Enabled || len(info.Sites) != 1 || info.Sites[0].Name != "peer" || info.Sites[0].Endpoint != "http://localhost:9002" {
		t.Errorf("unexpected info: %+v", info)
	}
}

func TestSiteReplicationResyncOp(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"op":"start","id":"abc","status":"success","state":"pending"}`))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	resync, err := c.SiteReplicationResyncOp(context.Background(), "start", SiteReplicationPeer{Name: "peer", DeploymentID: "dep1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/resync/op") {
		t.Errorf("wrong path: %s", gotPath)
	}
	if !strings.Contains(gotPath, "operation=start") {
		t.Errorf("expected operation=start, got: %s", gotPath)
	}
	if resync.OpType != "start" || resync.Status != "success" {
		t.Errorf("unexpected resync: %+v", resync)
	}
}

// repairPreflightReply is the dry-run response shape: camelCase fields, a
// site map keyed by deployment ID, and the "replication" family.
const repairPreflightReply = `{"mode":"dry-run","status":"planned","preflightToken":"preflight-token",` +
	`"retryEvents":2,"sites":{"dep-peer":{"deploymentId":"dep-peer","name":"peer",` +
	`"families":{"replication":{"planned":1,"succeeded":0,"failed":0,"retryEvents":2,` +
	`"tasks":[{"taskId":"task-1","status":"planned"}],"errors":[]}}}}}`

func TestSiteReplicationRepairPreflight(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, readRequestBody(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(repairPreflightReply))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	preflight, err := c.SiteReplicationRepairPreflight(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut || !strings.Contains(gotPath, "/site-replication/repair") {
		t.Errorf("preflight sent %s %s, want PUT /site-replication/repair", gotMethod, gotPath)
	}
	if gotBody != `{"mode":"dry-run"}` {
		t.Errorf("preflight body = %s, want a dry-run request", gotBody)
	}
	if preflight.PreflightToken != "preflight-token" || preflight.RetryEvents != 2 {
		t.Errorf("unexpected preflight: %+v", preflight)
	}
	family := preflight.Sites["dep-peer"].Families["replication"]
	if family.Planned != 1 || family.Succeeded != 0 || family.RetryEvents != 2 {
		t.Errorf("unexpected family progress: %+v", family)
	}
	if len(family.Tasks) != 1 || family.Tasks[0].TaskID != "task-1" || family.Tasks[0].Status != "planned" {
		t.Errorf("unexpected tasks: %+v", family.Tasks)
	}
}

func TestSiteReplicationRepairExecute(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotBody = r.URL.Path, readRequestBody(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"execute","operationId":"op-1","status":"partial",` +
			`"sites":{"dep-peer":{"deploymentId":"dep-peer","name":"peer","families":{"replication":` +
			`{"planned":2,"succeeded":1,"failed":1,"retryEvents":0,` +
			`"tasks":[{"taskId":"task-1","status":"succeeded"},{"taskId":"task-2","status":"failed","error":"denied"}],` +
			`"errors":["denied"]}}}},"createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:05Z",` +
			`"completedAt":"2026-10-20T10:00:05Z"}`))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	operation, err := c.SiteReplicationRepairExecute(context.Background(), "preflight-token", "op-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/repair") {
		t.Errorf("wrong path: %s", gotPath)
	}
	wantBody := `{"mode":"execute","preflightToken":"preflight-token","operationId":"op-1"}`
	if gotBody != wantBody {
		t.Errorf("execute body = %s, want %s", gotBody, wantBody)
	}
	if operation.OperationID != "op-1" || operation.Status != "partial" {
		t.Errorf("unexpected operation: %+v", operation)
	}
	if operation.CompletedAt != "2026-10-20T10:00:05Z" {
		t.Errorf("completedAt = %q, want the RFC 3339 timestamp", operation.CompletedAt)
	}
	family := operation.Sites["dep-peer"].Families["replication"]
	if family.Failed != 1 || len(family.Errors) != 1 || family.Errors[0] != "denied" {
		t.Errorf("unexpected family progress: %+v", family)
	}
	if len(family.Tasks) != 2 || family.Tasks[1].Error != "denied" {
		t.Errorf("unexpected tasks: %+v", family.Tasks)
	}
}

func TestSiteReplicationRepairStatus(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"execute","operationId":"op-1","status":"success",` +
			`"sites":{"dep-peer":{"deploymentId":"dep-peer","name":"peer","families":{"replication":` +
			`{"planned":1,"succeeded":1,"failed":0,"retryEvents":0,"tasks":[],"errors":[]}}}}` +
			`,"createdAt":"2026-10-20T10:00:00Z"}`))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	operation, err := c.SiteReplicationRepairStatus(context.Background(), "op-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet || !strings.Contains(gotPath, "/site-replication/repair/status") {
		t.Errorf("status sent %s %s, want GET /site-replication/repair/status", gotMethod, gotPath)
	}
	if gotQuery != "operation-id=op-1" {
		t.Errorf("status query = %q, want operation-id=op-1", gotQuery)
	}
	if operation.Status != "success" {
		t.Errorf("status = %q, want success", operation.Status)
	}
	if operation.CompletedAt != "" {
		t.Errorf("completedAt = %q, want empty for an absent field", operation.CompletedAt)
	}
}

func TestSiteReplicationRepairErrors(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		body   string
		detail string
	}{
		{
			name:   "site replication is not configured",
			code:   "InvalidRequest",
			body:   "site replication is not configured",
			detail: "site replication is not configured",
		},
		{
			name:   "stale preflight",
			code:   "PreconditionFailed",
			body:   "site replication repair preflight is stale",
			detail: "preflight is stale",
		},
		{
			name:   "operation id bound to another preflight",
			code:   "ClientTokenConflict",
			body:   "repair operation ID is already bound to a different preflight",
			detail: "already bound to a different preflight",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + tc.code +
					`</Code><Message>` + tc.body + `</Message></Error>`))
			}))
			defer server.Close()
			c := testSiteReplicationClient(server)

			_, err := c.SiteReplicationRepairExecute(context.Background(), "token", "op-1")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !IsAPIErrorCode(err, tc.code) {
				t.Errorf("error code = %v, want %s", err, tc.code)
			}
			if !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("error = %q, want detail %q", err.Error(), tc.detail)
			}
		})
	}
}

// TestSiteReplicationRepairRequestErrors covers the transport failures of the
// preflight and status calls, which the provider reports as read/create errors.
func TestSiteReplicationRepairRequestErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("repair is busy"))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	if _, err := c.SiteReplicationRepairPreflight(context.Background()); err == nil || !strings.Contains(err.Error(), "repair is busy") {
		t.Errorf("preflight error = %v, want the server message", err)
	}
	if _, err := c.SiteReplicationRepairStatus(context.Background(), "op-1"); err == nil || !strings.Contains(err.Error(), "repair is busy") {
		t.Errorf("status error = %v, want the server message", err)
	}
}

func testSiteReplicationClient(server *httptest.Server) RustfsAdmin {
	c := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	c.accessSecret = "secret"
	return c
}

func readRequestBody(r *http.Request) string {
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// siteReplicationStatusReply mirrors the wire shape of
// site-replication/status: sites keyed by deployment ID (PeerInfo values),
// PascalCase summary counters, peer errors and the retry queue.
const siteReplicationStatusReply = `{"enabled":true,"MaxBuckets":7,"MaxUsers":3,"MaxGroups":2,` +
	`"MaxPolicies":4,"MaxILMExpiryRules":1,` +
	`"Sites":{"dep-a":{"name":"alpha","endpoint":"http://alpha:9000","deploymentID":"dep-a",` +
	`"sync":"enable","skipTlsVerify":true,"replicate-ilm-expiry":true},` +
	`"dep-b":{"name":"beta","endpoint":"http://beta:9000","deploymentID":"dep-b",` +
	`"sync":"disable","skipTlsVerify":false,"replicate-ilm-expiry":false}},` +
	`"StatsSummary":{"dep-a":{"ReplicatedBuckets":7,"ReplicatedTags":2,"TotalBucketsCount":9,` +
	`"TotalCorsConfigCount":1,"apiVersion":"1"},"dep-b":{"ReplicatedBuckets":0}},` +
	`"PeerErrors":{"dep-b":{"name":"beta","endpoint":"http://beta:9000","error":"connection refused"}},` +
	`"RetryStats":{"pending":2,"failed":1,"lastError":"dial tcp: refused"},"apiVersion":"1"}`

func TestSiteReplicationStatus(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(siteReplicationStatusReply))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	status, err := c.SiteReplicationStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/site-replication/status") {
		t.Errorf("wrong path: %s", gotPath)
	}
	if !status.Enabled {
		t.Error("enabled = false, want true")
	}
	if status.MaxBuckets != 7 || status.MaxUsers != 3 || status.MaxGroups != 2 ||
		status.MaxPolicies != 4 || status.MaxILMExpiryRules != 1 {
		t.Errorf("max counters = %+v", status)
	}
	if status.APIVersion != "1" {
		t.Errorf("api version = %q, want 1", status.APIVersion)
	}

	alpha, ok := status.Sites["dep-a"]
	if !ok {
		t.Fatalf("sites missing dep-a: %+v", status.Sites)
	}
	if alpha.Name != "alpha" || alpha.Endpoint != "http://alpha:9000" ||
		alpha.SyncState != "enable" || !alpha.SkipTLSVerify || !alpha.ReplicateILMExpiry {
		t.Errorf("alpha = %+v", alpha)
	}
	beta, ok := status.Sites["dep-b"]
	if !ok || beta.SyncState != "disable" {
		t.Errorf("beta = %+v, want sync disable", beta)
	}

	summary, ok := status.StatsSummary["dep-a"]
	if !ok {
		t.Fatalf("stats summary missing dep-a: %+v", status.StatsSummary)
	}
	if summary.ReplicatedBuckets != 7 || summary.ReplicatedTags != 2 ||
		summary.TotalBucketsCount != 9 || summary.TotalCorsConfigCount != 1 {
		t.Errorf("dep-a summary = %+v", summary)
	}

	peerErr, ok := status.PeerErrors["dep-b"]
	if !ok {
		t.Fatalf("peer errors missing dep-b: %+v", status.PeerErrors)
	}
	if peerErr.Name != "beta" || peerErr.Error != "connection refused" {
		t.Errorf("peer error = %+v", peerErr)
	}

	if status.RetryStats == nil {
		t.Fatal("retry stats missing")
	}
	if status.RetryStats.Pending != 2 || status.RetryStats.Failed != 1 ||
		status.RetryStats.LastError != "dial tcp: refused" {
		t.Errorf("retry stats = %+v", status.RetryStats)
	}
}

func TestSiteReplicationStatusUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte("not implemented"))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	_, err := c.SiteReplicationStatus(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsUnsupportedFeature(err) {
		t.Fatalf("error = %v, want an unsupported-feature error", err)
	}
	if !strings.Contains(err.Error(), "site-replication/status") {
		t.Errorf("error does not name the route: %v", err)
	}
}

// siteReplicationMetaInfoReply carries the full SRInfo document with one
// bucket, one policy, two users, one group, one mapping of each kind and one
// ILM expiry rule.
const siteReplicationMetaInfoReply = `{"enabled":true,"name":"alpha","deploymentID":"dep-a",` +
	`"apiVersion":"1","buckets":{"photos":{}},"policies":{"readonly":{}},` +
	`"userPolicies":{"alice":{"policy":"readonly"}},"userInfoMap":{"alice":{},"bob":{}},` +
	`"groupDescMap":{"auditors":{}},"groupPolicies":{"auditors":{"policy":"readonly"}},` +
	`"replicationCfg":{"photos":{"role":"arn"}},` +
	`"ilmExpiryRules":{"rule-1":{"bucket":"photos"}},` +
	`"state":{"name":"alpha","peers":{"dep-a":{},"dep-b":{}},"updatedAt":"2026-01-02T03:04:05Z"}}`

func TestSiteReplicationMetaInfo(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(siteReplicationMetaInfoReply))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	info, err := c.SiteReplicationMetaInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/site-replication/metainfo") {
		t.Errorf("wrong path: %s", gotPath)
	}

	want := SiteReplicationMetaInfo{
		Enabled:                true,
		Name:                   "alpha",
		DeploymentID:           "dep-a",
		APIVersion:             "1",
		StateName:              "alpha",
		StateUpdatedAt:         "2026-01-02T03:04:05Z",
		PeerCount:              2,
		BucketCount:            1,
		PolicyCount:            1,
		UserCount:              2,
		GroupCount:             1,
		UserPolicyCount:        1,
		GroupPolicyCount:       1,
		ILMExpiryRuleCount:     1,
		ReplicationConfigCount: 1,
	}
	if info != want {
		t.Errorf("metainfo = %+v, want %+v", info, want)
	}
}

func TestSiteReplicationMetaInfoArrayILMExpiryRules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":true,"ilmExpiryRules":[{"bucket":"a"},{"bucket":"b"}]}`))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	info, err := c.SiteReplicationMetaInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ILMExpiryRuleCount != 2 {
		t.Errorf("ilm expiry rule count = %d, want 2 (array form)", info.ILMExpiryRuleCount)
	}
}

func TestSiteReplicationMetaInfoError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("access denied"))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	_, err := c.SiteReplicationMetaInfo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("error = %v, want the server message", err)
	}
}

func TestSiteReplicationResyncOpError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid deployment id"))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	_, err := c.SiteReplicationResyncOp(context.Background(), "start", SiteReplicationPeer{DeploymentID: "dep-a"})
	if err == nil || !strings.Contains(err.Error(), "invalid deployment id") {
		t.Fatalf("error = %v, want the server message", err)
	}
}

func TestJSONCollectionLen(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{name: "empty", raw: "", want: 0},
		{name: "object", raw: `{"a":{},"b":{}}`, want: 2},
		{name: "array", raw: `[{},{}]`, want: 2},
		{name: "neither", raw: `"scalar"`, want: 0},
		{name: "invalid", raw: `{`, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonCollectionLen(json.RawMessage(tc.raw)); got != tc.want {
				t.Errorf("jsonCollectionLen(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}
