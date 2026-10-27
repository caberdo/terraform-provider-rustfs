// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newHandlerAdminClient points an admin client at an httptest server running
// handler, using the same request signing configuration as the provider.
func newHandlerAdminClient(t *testing.T, handler http.HandlerFunc) *RustfsAdmin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(&RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
	return &c
}

// newFailingAdminClient answers every request with an S3-style 500 body, so the
// `if err != nil` branch after each doRequest is exercised.
func newFailingAdminClient(t *testing.T) *RustfsAdmin {
	t.Helper()
	return newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `<Error><Code>InternalError</Code><Message>boom</Message></Error>`)
	})
}

// newInvalidJSONAdminClient answers 200 with a body no decoder can parse.
func newInvalidJSONAdminClient(t *testing.T) *RustfsAdmin {
	t.Helper()
	return newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{not json`)
	})
}

// TestAdminClientErrorBranchesOnServerFailure covers the `doRequest` error
// branch of every client call that returns it unchanged.
func TestAdminClientErrorBranchesOnServerFailure(t *testing.T) {
	client := newFailingAdminClient(t)
	raw := json.RawMessage(`{"tier_type":"s3"}`)

	cases := []struct {
		name string
		call func() error
	}{
		{"AddTier", func() error { return client.AddTier(context.Background(), raw) }},
		{"AttachLDAPPolicy", func() error {
			return client.AttachLDAPPolicy(context.Background(), LDAPPolicyAttachment{UserOrGroup: "cn=alice", PolicyName: "readwrite"})
		}},
		{"AttachPolicyToUser", func() error { return client.AttachPolicyToUser(context.Background(), "alice", "readwrite") }},
		{"CreateKmsKey", func() error { _, err := client.CreateKmsKey(context.Background(), "k1"); return err }},
		{"CreatePolicy", func() error { return client.CreatePolicy(context.Background(), Policy{Name: "p"}) }},
		{"CreateServiceAccount", func() error {
			return client.CreateServiceAccount(context.Background(), ServiceAccount{AccessKey: "sa1"})
		}},
		{"CreateUserAccount", func() error { return client.CreateUserAccount(context.Background(), UserAccount{AccessKey: "alice"}) }},
		{"DeleteBucketDurability", func() error { return client.DeleteBucketDurability(context.Background(), "b") }},
		{"DeleteConfig", func() error { return client.DeleteConfig(context.Background(), "scanner") }},
		{"DeleteGroup", func() error { return client.DeleteGroup(context.Background(), "devs") }},
		{"DeleteKmsKey", func() error { return client.DeleteKmsKey(context.Background(), "k1") }},
		{"DeletePolicy", func() error { return client.DeletePolicy(context.Background(), "p") }},
		{"DeleteRemoteTarget", func() error { return client.DeleteRemoteTarget(context.Background(), "b", "arn") }},
		{"DeleteServiceAccount", func() error {
			return client.DeleteServiceAccount(context.Background(), ServiceAccount{AccessKey: "sa1"})
		}},
		{"DeleteUserAccount", func() error { return client.DeleteUserAccount(context.Background(), UserAccount{AccessKey: "alice"}) }},
		{"DescribeKmsKey", func() error { _, err := client.DescribeKmsKey(context.Background(), "k1"); return err }},
		{"DetachLDAPPolicy", func() error {
			return client.DetachLDAPPolicy(context.Background(), LDAPPolicyAttachment{UserOrGroup: "cn=alice", PolicyName: "readwrite"})
		}},
		{"DisableKmsKey", func() error { return client.DisableKmsKey(context.Background(), "k1") }},
		{"EditTier", func() error { return client.EditTier(context.Background(), "COLD", raw) }},
		{"EnableKmsKey", func() error { return client.EnableKmsKey(context.Background(), "k1") }},
		{"ExportBucketMetadata", func() error { _, err := client.ExportBucketMetadata(context.Background()); return err }},
		{"ExportIam", func() error { _, err := client.ExportIam(context.Background()); return err }},
		{"GetBucketDurability", func() error { _, err := client.GetBucketDurability(context.Background(), "b"); return err }},
		{"GetGroup", func() error { _, err := client.GetGroup(context.Background(), "devs"); return err }},
		{"GetHealthInfo", func() error { _, err := client.GetHealthInfo(context.Background()); return err }},
		{"GetModuleSwitches", func() error { _, err := client.GetModuleSwitches(context.Background()); return err }},
		{"GetObdInfo", func() error { _, err := client.GetObdInfo(context.Background()); return err }},
		{"ImportBucketMetadata", func() error { return client.ImportBucketMetadata(context.Background(), []byte("zip")) }},
		{"ImportIam", func() error { return client.ImportIam(context.Background(), []byte("zip")) }},
		{"ListAuditTargets", func() error { _, err := client.ListAuditTargets(context.Background()); return err }},
		{"ListCannedPolicies", func() error { _, err := client.ListCannedPolicies(context.Background()); return err }},
		{"ListGroups", func() error { _, err := client.ListGroups(context.Background()); return err }},
		{"ListKmsKeys", func() error { _, err := client.ListKmsKeys(context.Background()); return err }},
		{"ListNotificationTargets", func() error { _, err := client.ListNotificationTargets(context.Background()); return err }},
		{"ListPools", func() error { _, err := client.ListPools(context.Background()); return err }},
		{"ListRemoteTargets", func() error { _, err := client.ListRemoteTargets(context.Background(), "b"); return err }},
		{"ListUsers", func() error { _, err := client.ListUsers(context.Background(), ""); return err }},
		{"ReadPolicy", func() error { _, err := client.ReadPolicy(context.Background(), "p"); return err }},
		{"ReadQuota", func() error { _, err := client.ReadQuota(context.Background(), "b"); return err }},
		{"ReadServiceAccount", func() error { _, err := client.ReadServiceAccount(context.Background(), "sa1"); return err }},
		{"ReadUserAccount", func() error { _, err := client.ReadUserAccount(context.Background(), "alice"); return err }},
		{"ReadUserMFA", func() error { _, err := client.ReadUserMFA(context.Background(), "alice"); return err }},
		{"RemoveTier", func() error { return client.RemoveTier(context.Background(), "COLD") }},
		{"ResetAuditTarget", func() error { return client.ResetAuditTarget(context.Background(), "webhook", "primary") }},
		{"ResetNotificationTarget", func() error { return client.ResetNotificationTarget(context.Background(), "amqp", "primary") }},
		{"RotateKmsKey", func() error { _, err := client.RotateKmsKey(context.Background(), "k1"); return err }},
		{"ServerInfo", func() error { _, err := client.ServerInfo(context.Background()); return err }},
		{"SetAuditTarget", func() error { return client.SetAuditTarget(context.Background(), "webhook", "primary", nil) }},
		{"SetBucketDurability", func() error {
			_, err := client.SetBucketDurability(context.Background(), "b", BucketDurability{Bucket: "b", Mode: "strict"})
			return err
		}},
		{"SetConfig", func() error { return client.SetConfig(context.Background(), "scanner", nil) }},
		{"SetGroupStatus", func() error { return client.SetGroupStatus(context.Background(), "devs", "disabled") }},
		{"SetModuleSwitches", func() error {
			_, err := client.SetModuleSwitches(context.Background(), ModuleSwitchUpdate{})
			return err
		}},
		{"SetNotificationTarget", func() error { return client.SetNotificationTarget(context.Background(), "amqp", "primary", nil) }},
		{"SetQuota", func() error { _, err := client.SetQuota(context.Background(), Quota{Bucket: "b"}); return err }},
		{"DeletQuota", func() error { return client.DeletQuota(context.Background(), "b") }},
		{"SetUserSecretKey", func() error { return client.SetUserSecretKey(context.Background(), "alice", "s3cret") }},
		{"SetUserStatus", func() error { return client.SetUserStatus(context.Background(), "alice", "disabled") }},
		{"SiteReplicationAdd", func() error {
			return client.SiteReplicationAdd(context.Background(), SiteReplicationSite{Name: "peer"})
		}},
		{"EditPeer", func() error { return client.EditPeer(context.Background(), SiteReplicationPeerEdit{Name: "peer"}) }},
		{"SiteReplicationInfo", func() error { _, err := client.SiteReplicationInfo(context.Background()); return err }},
		{"RemovePeer", func() error { return client.RemovePeer(context.Background(), []string{"peer"}) }},
		{"SiteReplicationResyncOp", func() error {
			_, err := client.SiteReplicationResyncOp(context.Background(), "start", SiteReplicationPeer{Name: "peer"})
			return err
		}},
		{"StartRebalance", func() error { _, err := client.StartRebalance(context.Background()); return err }},
		{"RebalanceStatus", func() error { _, err := client.RebalanceStatus(context.Background()); return err }},
		{"StopRebalance", func() error { return client.StopRebalance(context.Background()) }},
		{"StorageInfo", func() error { _, err := client.StorageInfo(context.Background()); return err }},
		{"TierStats", func() error { _, err := client.TierStats(context.Background()); return err }},
		{"UpdateGroupMembers", func() error {
			return client.UpdateGroupMembers(context.Background(), GroupAddRemove{Group: "devs", Members: []string{"alice"}})
		}},
		{"UpdateServiceAccount", func() error {
			return client.UpdateServiceAccount(context.Background(), ServiceAccount{AccessKey: "sa1"})
		}},
		{"AddRemoteTarget", func() error {
			_, err := client.AddRemoteTarget(context.Background(), "b", RemoteTarget{Type: "replication"})
			return err
		}},
		{"ReplicationMetrics", func() error { _, err := client.ReplicationMetrics(context.Background(), "b"); return err }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("%s: expected an error from a 500 response", tc.name)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("%s: expected an *APIError, got %T: %v", tc.name, err, err)
			}
			if apiErr.StatusCode != http.StatusInternalServerError || apiErr.Code != "InternalError" {
				t.Fatalf("%s: unexpected API error: %+v", tc.name, apiErr)
			}
		})
	}
}

// TestAdminClientDecodeErrorBranches covers the JSON decode branch of every
// call that decodes a response body.
func TestAdminClientDecodeErrorBranches(t *testing.T) {
	client := newInvalidJSONAdminClient(t)

	cases := []struct {
		name string
		call func() error
	}{
		{"CreateKmsKey", func() error { _, err := client.CreateKmsKey(context.Background(), "k1"); return err }},
		{"DescribeKmsKey", func() error { _, err := client.DescribeKmsKey(context.Background(), "k1"); return err }},
		{"DisableKmsKey", func() error { return client.DisableKmsKey(context.Background(), "k1") }},
		{"EnableKmsKey", func() error { return client.EnableKmsKey(context.Background(), "k1") }},
		{"GetBucketDurability", func() error { _, err := client.GetBucketDurability(context.Background(), "b"); return err }},
		{"GetKmsConfig", func() error { _, err := client.GetKmsConfig(context.Background()); return err }},
		{"GetKmsStatus", func() error { _, err := client.GetKmsStatus(context.Background()); return err }},
		{"GetModuleSwitches", func() error { _, err := client.GetModuleSwitches(context.Background()); return err }},
		{"GetObdInfo", func() error { _, err := client.GetObdInfo(context.Background()); return err }},
		{"ListAuditTargets", func() error { _, err := client.ListAuditTargets(context.Background()); return err }},
		{"ListKmsKeys", func() error { _, err := client.ListKmsKeys(context.Background()); return err }},
		{"ListNotificationTargets", func() error { _, err := client.ListNotificationTargets(context.Background()); return err }},
		{"ListRemoteTargets", func() error { _, err := client.ListRemoteTargets(context.Background(), "b"); return err }},
		{"ListUsers", func() error { _, err := client.ListUsers(context.Background(), ""); return err }},
		{"ReadPolicy", func() error { _, err := client.ReadPolicy(context.Background(), "p"); return err }},
		{"ReadQuota", func() error { _, err := client.ReadQuota(context.Background(), "b"); return err }},
		{"ReadServiceAccount", func() error { _, err := client.ReadServiceAccount(context.Background(), "sa1"); return err }},
		{"ReadUserAccount", func() error { _, err := client.ReadUserAccount(context.Background(), "alice"); return err }},
		{"ReadUserMFA", func() error { _, err := client.ReadUserMFA(context.Background(), "alice"); return err }},
		{"ReplicationMetrics", func() error { _, err := client.ReplicationMetrics(context.Background(), "b"); return err }},
		{"RotateKmsKey", func() error { _, err := client.RotateKmsKey(context.Background(), "k1"); return err }},
		{"ServerInfo", func() error { _, err := client.ServerInfo(context.Background()); return err }},
		{"SetBucketDurability", func() error {
			_, err := client.SetBucketDurability(context.Background(), "b", BucketDurability{})
			return err
		}},
		{"SetModuleSwitches", func() error {
			_, err := client.SetModuleSwitches(context.Background(), ModuleSwitchUpdate{})
			return err
		}},
		{"SetQuota", func() error { _, err := client.SetQuota(context.Background(), Quota{Bucket: "b"}); return err }},
		{"SiteReplicationInfo", func() error { _, err := client.SiteReplicationInfo(context.Background()); return err }},
		{"SiteReplicationResyncOp", func() error {
			_, err := client.SiteReplicationResyncOp(context.Background(), "status", SiteReplicationPeer{Name: "peer"})
			return err
		}},
		{"StorageInfo", func() error { _, err := client.StorageInfo(context.Background()); return err }},
		{"TierStats", func() error { _, err := client.TierStats(context.Background()); return err }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("%s: expected a decode error", tc.name)
			}
			var syntaxErr *json.SyntaxError
			if !errors.As(err, &syntaxErr) {
				t.Fatalf("%s: expected a *json.SyntaxError, got %T: %v", tc.name, err, err)
			}
		})
	}
}

// errReadCloser fails every read while behaving like a closable body.
type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("transport read failed") }
func (errReadCloser) Close() error             { return nil }

// roundTripFunc adapts a function to an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestAdminClientReadErrorBranches covers the io.ReadAll branch of the calls
// that read a whole body before parsing it.
func TestAdminClientReadErrorBranches(t *testing.T) {
	c := New(&RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     "example.invalid:9000",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       errReadCloser{},
			}, nil
		})},
	})

	cases := []struct {
		name string
		call func() error
	}{
		{"GetConfig", func() error { _, err := c.GetConfig(context.Background(), "scanner"); return err }},
		{"ListCannedPolicies", func() error { _, err := c.ListCannedPolicies(context.Background()); return err }},
		{"ListGroups", func() error { _, err := c.ListGroups(context.Background()); return err }},
		{"AddRemoteTarget", func() error {
			_, err := c.AddRemoteTarget(context.Background(), "b", RemoteTarget{Type: "replication"})
			return err
		}},
		{"ReplicationMetrics", func() error { _, err := c.ReplicationMetrics(context.Background(), "b"); return err }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("%s: expected a body read error", tc.name)
			}
			if !strings.Contains(err.Error(), "transport read failed") {
				t.Fatalf("%s: expected the transport error, got %v", tc.name, err)
			}
		})
	}
}

// TestListCannedPoliciesUnrecognizedResponse covers the fallback error for a
// body that is neither a JSON array nor a JSON object.
func TestListCannedPoliciesUnrecognizedResponse(t *testing.T) {
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `"just a string"`)
	})
	if _, err := client.ListCannedPolicies(context.Background()); err == nil {
		t.Fatal("expected an unrecognized-response error")
	}
}

// TestListGroupsUnrecognizedResponse covers the fallback error of ListGroups.
func TestListGroupsUnrecognizedResponse(t *testing.T) {
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `42`)
	})
	if _, err := client.ListGroups(context.Background()); err == nil {
		t.Fatal("expected an unrecognized-response error")
	}
}

// TestUpdateGroupMembersNilMembers covers the nil-members normalization: the
// request must still carry an (empty) members array.
func TestUpdateGroupMembersNilMembers(t *testing.T) {
	var body string
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusOK)
	})
	if err := client.UpdateGroupMembers(context.Background(), GroupAddRemove{Group: "devs"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(body, `"members":[]`) {
		t.Errorf("expected an empty members array, got %s", body)
	}
}

// TestGetConfigSingleTokenLine covers the tolerance for a rendered line with no
// assignments: it contributes no key/value pairs instead of failing.
func TestGetConfigSingleTokenLine(t *testing.T) {
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "scanner\nscanner delay=\"5s\"")
	})
	kvs, err := client.GetConfig(context.Background(), "scanner")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(kvs) != 1 || kvs[0].Key != "delay" || kvs[0].Value != "5s" {
		t.Errorf("unexpected kvs: %+v", kvs)
	}
}

// TestDoDirectRequestQueryAndSuccess covers the query, content-length and
// success branches of DoDirectRequest.
func TestDoDirectRequestQueryAndSuccess(t *testing.T) {
	var gotPath, gotQuery string
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})

	resp, err := client.DoDirectRequest(context.Background(), RequestData{
		Method:      "PUT",
		RelPath:     "some/direct",
		Content:     []byte("payload"),
		QueryValues: map[string][]string{"q": {"a b"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("unexpected status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("unexpected body: %s", body)
	}
	if gotPath != "/some/direct" {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if gotQuery != "q=a%20b" {
		t.Errorf("query values must be space-escaped, got %q", gotQuery)
	}
}

// TestDoDirectRequestNoContent204 covers the empty-body 204 success path.
func TestDoDirectRequestNoContent204(t *testing.T) {
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	resp, err := client.DoDirectRequest(context.Background(), RequestData{Method: "DELETE", RelPath: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer drainClose(resp)
}

// TestDoDirectRequestNetworkError covers the transport error branch.
func TestDoDirectRequestNetworkError(t *testing.T) {
	c := New(&RustfsAdminConfig{Endpoint: "127.0.0.1:1", AccessKey: "a", AccessSecret: "b"})
	resp, err := c.DoDirectRequest(context.Background(), RequestData{Method: "GET", RelPath: "x"})
	defer drainClose(resp)
	if err == nil {
		t.Fatal("expected a network error")
	}
}

// TestCreateRootUrl covers the default-port stripping and scheme choice.
func TestCreateRootUrl(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		secure   bool
		want     string
	}{
		{"secure strips 443", "host:443", true, "https://host"},
		{"secure keeps other port", "host:9000", true, "https://host:9000"},
		{"insecure strips 80", "host:80", false, "http://host"},
		{"insecure keeps other port", "host:9000", false, "http://host:9000"},
		{"https scheme wins over the flag", "https://host:9000", false, "https://host:9000"},
		{"http scheme wins over the flag", "http://host:443", true, "http://host:443"},
		{"scheme is not doubled", "https://host", true, "https://host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RootURL(tt.endpoint, tt.secure); got != tt.want {
				t.Errorf("RootURL(%q, %v) = %q, want %q", tt.endpoint, tt.secure, got, tt.want)
			}
		})
	}
}

// TestCreateRequestError covers the invalid-request branch, which doRequest
// surfaces unchanged.
func TestCreateRequestError(t *testing.T) {
	c := RustfsAdmin{rootURL: "http://host", adminBaseURL: "http://host/rustfs/admin", accessKey: "a", accessSecret: "b", httpClient: &http.Client{}}
	if _, err := c.createRequest(context.Background(), RequestData{Method: "BAD METHOD", RelPath: "x"}); err == nil {
		t.Fatal("expected an invalid request error")
	}
	resp, err := c.doRequest(context.Background(), RequestData{Method: "BAD METHOD", RelPath: "x"})
	defer drainClose(resp)
	if err == nil {
		t.Fatal("expected doRequest to surface the invalid request error")
	}
	directResp, err := c.DoDirectRequest(context.Background(), RequestData{Method: "BAD METHOD", RelPath: "x"})
	defer drainClose(directResp)
	if err == nil {
		t.Fatal("expected DoDirectRequest to surface the invalid request error")
	}
}

// TestKmsKeyWithoutMetadata covers the responses where the server reports a key
// id and message but no key_metadata object.
func TestKmsKeyWithoutMetadata(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"success":true,"message":"created","key_id":"k1"}`)
		})
		key, err := client.CreateKmsKey(context.Background(), "k1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key.KeyID != "k1" || key.KeyState != "Enabled" {
			t.Errorf("unexpected key: %+v", key)
		}
		if key.Tags["name"] != "k1" {
			t.Errorf("expected the requested name tag, got %+v", key.Tags)
		}
	})

	t.Run("describe missing metadata", func(t *testing.T) {
		client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"success":false,"message":"key not found","key_metadata":null}`)
		})
		if _, err := client.DescribeKmsKey(context.Background(), "k1"); err == nil || err.Error() != "key not found" {
			t.Fatalf("expected the server message as the error, got %v", err)
		}
	})

	t.Run("rotate missing metadata", func(t *testing.T) {
		client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"success":false,"message":"unsupported capability","key_metadata":null}`)
		})
		if _, err := client.RotateKmsKey(context.Background(), "k1"); err == nil || err.Error() != "unsupported capability" {
			t.Fatalf("expected the server message as the error, got %v", err)
		}
	})

	t.Run("lifecycle failure", func(t *testing.T) {
		client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"success":false,"message":"key is already disabled","key_metadata":null}`)
		})
		if err := client.DisableKmsKey(context.Background(), "k1"); err == nil || err.Error() != "key is already disabled" {
			t.Fatalf("expected the server message as the error, got %v", err)
		}
	})
}

// TestPolicyConditionUnmarshalBranches covers the condition decoder's error,
// null and scalar branches that a decoded policy document cannot reach.
func TestPolicyConditionUnmarshalBranches(t *testing.T) {
	var condition PolicyCondition
	if err := json.Unmarshal([]byte(`"not an object"`), &condition); err == nil {
		t.Fatal("expected an error for a non-object condition")
	}

	condition = PolicyCondition{"StringLike": {"s3:prefix": {"tenant-42/*"}}}
	if err := json.Unmarshal([]byte(`null`), &condition); err != nil {
		t.Fatalf("null condition: %v", err)
	}
	if condition != nil {
		t.Errorf("a null condition must clear the map, got %+v", condition)
	}

	if _, err := policyConditionScalar(json.RawMessage(`{`)); err == nil {
		t.Fatal("expected an error for an undecodable condition value")
	}

	values, err := policyConditionValues(json.RawMessage(`null`))
	if err != nil {
		t.Fatalf("null condition value: %v", err)
	}
	if len(values) != 1 || values[0] != "" {
		t.Errorf("a null condition value must normalize to one empty string, got %v", values)
	}
}

// TestReadPolicyMalformedDocument covers the errors.Join path of
// parsePolicyField, surfaced through ReadPolicy.
func TestReadPolicyMalformedDocument(t *testing.T) {
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"policy_name":"p","policy":{"Statement":[{"Action":123}]}}`)
	})
	if _, err := client.ReadPolicy(context.Background(), "p"); err == nil {
		t.Fatal("expected a malformed-document error")
	}
}

// TestPolicyParsersRejectMalformedJSON covers the unmarshal errors of the
// condition-value and quoted-document parsers.
func TestPolicyParsersRejectMalformedJSON(t *testing.T) {
	if _, err := policyConditionValues(json.RawMessage(`[`)); err == nil {
		t.Fatal("expected a malformed condition array to fail")
	}
	if _, err := parsePolicyField(json.RawMessage(`"unterminated`)); err == nil {
		t.Fatal("expected a malformed quoted document to fail")
	}
}

// TestParseServerVersionPrereleaseWithBuildMetadata covers stripping build
// metadata from the prerelease part.
func TestParseServerVersionPrereleaseWithBuildMetadata(t *testing.T) {
	version, err := ParseServerVersion("1.0.0-rc.5+build.9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version.Prerelease != "rc.5" {
		t.Errorf("prerelease = %q, want rc.5", version.Prerelease)
	}
}

// TestVersionComparePrereleaseOrdering covers comparing two distinct
// prereleases of the same release (compared as strings, not numerically).
func TestVersionComparePrereleaseOrdering(t *testing.T) {
	older, err := ParseServerVersion("1.0.0-rc.5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	newer, err := ParseServerVersion("1.0.0-rc.9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := older.Compare(newer); got != -1 {
		t.Errorf("rc.5.Compare(rc.9) = %d, want -1", got)
	}
	if got := newer.Compare(older); got != 1 {
		t.Errorf("rc.9.Compare(rc.5) = %d, want 1", got)
	}
}

// TestUnsupportedFeatureErrorMessageBranches covers the route fallbacks and the
// minimum-version wording.
func TestUnsupportedFeatureErrorMessageBranches(t *testing.T) {
	withoutRoute := &UnsupportedFeatureError{Feature: Feature("audit"), ServerVersion: "1.0.3"}
	message := withoutRoute.Error()
	if !strings.Contains(message, "audit") || !strings.Contains(message, "does not implement audit") {
		t.Errorf("unexpected message: %q", message)
	}

	minVersion, err := ParseServerVersion("1.0.5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	withMinVersion := &UnsupportedFeatureError{Feature: FeatureMetrics, Route: "GET /x", MinVersion: &minVersion}
	if message := withMinVersion.Error(); !strings.Contains(message, "requires RustFS 1.0.5 or later") {
		t.Errorf("unexpected message: %q", message)
	}

	registered := &UnsupportedFeatureError{Feature: FeatureMetrics}
	if message := registered.Error(); !strings.Contains(message, "GET /rustfs/admin/v3/metrics") {
		t.Errorf("a registered feature must fall back to its route, got %q", message)
	}
}

// TestRegisterFeatureRejectsDuplicate covers the programming-error guard that
// keeps one spec per feature.
func TestRegisterFeatureRejectsDuplicate(t *testing.T) {
	feature := Feature("duplicate-test-feature")
	registerFeature(feature, featureSpec{route: "GET /duplicate"})
	t.Cleanup(func() {
		featureMu.Lock()
		delete(featureSpecs, feature)
		featureMu.Unlock()
	})
	defer func() {
		if recover() == nil {
			t.Fatal("registering the same feature twice must panic")
		}
	}()

	registerFeature(feature, featureSpec{route: "GET /duplicate-again"})
}

// TestIsUnsupportedRouteIgnoresPlainErrors covers the non-APIError branch.
func TestIsUnsupportedRouteIgnoresPlainErrors(t *testing.T) {
	if isUnsupportedRoute(errors.New("plain error")) {
		t.Error("a plain error must not be reported as an unsupported route")
	}
}

// TestServerVersionMissingVersion covers a 200 healthinfo answer without a
// version field.
func TestServerVersionMissingVersion(t *testing.T) {
	client := newHandlerAdminClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	})
	_, _, err := client.ServerVersion(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not report a version") {
		t.Fatalf("expected a missing-version error, got %v", err)
	}
}
