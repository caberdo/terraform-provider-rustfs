// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func newTestPolicyServer(t *testing.T, handler http.HandlerFunc) RustfsAdmin {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"
	return client
}

func TestListPoliciesObjectSlice(t *testing.T) {
	client := newTestPolicyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/list-canned-policies" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"policy_name":"readwrite"},{"policy_name":"readonly"}]`))
	})

	names, err := client.ListCannedPolicies(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"readonly", "readwrite"}) {
		t.Errorf("wrong names: %v", names)
	}
}

func TestListPoliciesStringSlice(t *testing.T) {
	client := newTestPolicyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rustfs/admin/v3/list-canned-policies" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["readwrite","readonly"]`))
	})

	names, err := client.ListCannedPolicies(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"readonly", "readwrite"}) {
		t.Errorf("wrong names: %v", names)
	}
}

func TestListPoliciesMap(t *testing.T) {
	client := newTestPolicyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rustfs/admin/v3/list-canned-policies" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"readwrite":{},"readonly":{}}`))
	})

	names, err := client.ListCannedPolicies(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"readonly", "readwrite"}) {
		t.Errorf("wrong names: %v", names)
	}
}

func TestGetPolicy(t *testing.T) {
	client := newTestPolicyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/info-canned-policy" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "readwrite" {
			t.Errorf("expected name=readwrite, got %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"policy_name": "readwrite",
			"policy": {
				"Version": "2012-10-17",
				"Statement": [
					{
						"Effect": "Allow",
						"Action": ["s3:GetObject", "s3:ListBucket"],
						"Resource": ["arn:aws:s3:::*"]
					}
				]
			}
		}`))
	})

	policy, err := client.ReadPolicy(context.Background(), "readwrite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy.Name != "readwrite" {
		t.Errorf("expected name readwrite, got %s", policy.Name)
	}
	if policy.Version != "2012-10-17" {
		t.Errorf("expected version 2012-10-17, got %s", policy.Version)
	}
	if len(policy.Statement) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(policy.Statement))
	}
	stmt := policy.Statement[0]
	if stmt.Effect != "Allow" {
		t.Errorf("expected effect Allow, got %s", stmt.Effect)
	}
	if !reflect.DeepEqual(stmt.Action, []string{"s3:GetObject", "s3:ListBucket"}) {
		t.Errorf("wrong actions: %v", stmt.Action)
	}
	if !reflect.DeepEqual(stmt.Resource, []string{"arn:aws:s3:::*"}) {
		t.Errorf("wrong resources: %v", stmt.Resource)
	}
}

func TestGetPolicyStatementFields(t *testing.T) {
	client := newTestPolicyServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"policy_name": "tenant",
			"policy": {
				"Version": "2012-10-17",
				"Statement": [
					{
						"Effect": "Allow",
						"Sid": "PrefixAccess",
						"Action": ["s3:ListBucket"],
						"Resource": ["arn:aws:s3:::app-data"],
						"Condition": {
							"StringLike": {"s3:prefix": ["tenant-42/*"]},
							"StringEquals": {"s3:x-amz-acl": "private"}
						}
					},
					{
						"Effect": "Allow",
						"NotAction": ["sts:AssumeRole"],
						"NotResource": ["arn:aws:s3:::other"],
						"Condition": {
							"Bool": {"aws:SecureTransport": true},
							"NumericLessThanEquals": {"s3:max-keys": 10}
						}
					}
				]
			}
		}`))
	})

	policy, err := client.ReadPolicy(context.Background(), "tenant")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(policy.Statement) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(policy.Statement))
	}

	first := policy.Statement[0]
	if first.Sid != "PrefixAccess" {
		t.Errorf("expected Sid PrefixAccess, got %q", first.Sid)
	}
	wantFirstCondition := PolicyCondition{
		"StringLike":   {"s3:prefix": {"tenant-42/*"}},
		"StringEquals": {"s3:x-amz-acl": {"private"}},
	}
	if !reflect.DeepEqual(first.Condition, wantFirstCondition) {
		t.Errorf("wrong condition: %v", first.Condition)
	}
	if first.NotAction != nil || first.NotResource != nil {
		t.Errorf("expected no negations, got %v / %v", first.NotAction, first.NotResource)
	}

	second := policy.Statement[1]
	if second.Action != nil {
		t.Errorf("expected no Action, got %v", second.Action)
	}
	if !reflect.DeepEqual(second.NotAction, []string{"sts:AssumeRole"}) {
		t.Errorf("wrong NotAction: %v", second.NotAction)
	}
	if !reflect.DeepEqual(second.NotResource, []string{"arn:aws:s3:::other"}) {
		t.Errorf("wrong NotResource: %v", second.NotResource)
	}
	wantSecondCondition := PolicyCondition{
		"Bool":                  {"aws:SecureTransport": {"true"}},
		"NumericLessThanEquals": {"s3:max-keys": {"10"}},
	}
	if !reflect.DeepEqual(second.Condition, wantSecondCondition) {
		t.Errorf("wrong normalized condition: %v", second.Condition)
	}
}

func TestGetPolicyStringForm(t *testing.T) {
	document := `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Sid": "PrefixAccess",
				"Action": "s3:GetObject",
				"Resource": ["arn:aws:s3:::app-data/*"],
				"Condition": {"StringLike": {"s3:prefix": "tenant-42/*"}}
			},
			{
				"Effect": "Deny",
				"NotAction": "sts:AssumeRole",
				"NotResource": ["arn:aws:s3:::other"]
			}
		]
	}`
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}

	client := newTestPolicyServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"policy_name": "tenant", "policy": ` + string(encoded) + `}`))
	})

	policy, err := client.ReadPolicy(context.Background(), "tenant")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(policy.Statement) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(policy.Statement))
	}

	first := policy.Statement[0]
	if !reflect.DeepEqual(first.Action, []string{"s3:GetObject"}) {
		t.Errorf("wrong Action: %v", first.Action)
	}
	if first.Sid != "PrefixAccess" {
		t.Errorf("expected Sid PrefixAccess, got %q", first.Sid)
	}
	if !reflect.DeepEqual(first.Condition, PolicyCondition{"StringLike": {"s3:prefix": {"tenant-42/*"}}}) {
		t.Errorf("wrong condition: %v", first.Condition)
	}

	second := policy.Statement[1]
	if second.Action != nil {
		t.Errorf("expected no Action, got %v", second.Action)
	}
	if !reflect.DeepEqual(second.NotAction, []string{"sts:AssumeRole"}) {
		t.Errorf("wrong NotAction: %v", second.NotAction)
	}
	if !reflect.DeepEqual(second.NotResource, []string{"arn:aws:s3:::other"}) {
		t.Errorf("wrong NotResource: %v", second.NotResource)
	}
}

func TestCreatePolicyStatementFields(t *testing.T) {
	var body map[string]any
	client := newTestPolicyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if got := r.URL.Query().Get("name"); got != "tenant" {
			t.Errorf("expected name=tenant, got %s", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
	})

	err := client.CreatePolicy(context.Background(), Policy{
		Name: "tenant",
		Statement: []PolicyStatement{
			{
				Effect:    "Allow",
				Sid:       "PrefixAccess",
				Action:    []string{"s3:ListBucket"},
				Resource:  []string{"arn:aws:s3:::app-data"},
				Condition: PolicyCondition{"StringLike": {"s3:prefix": {"tenant-42/*"}}},
			},
			{
				Effect:      "Allow",
				NotAction:   []string{"sts:AssumeRole"},
				NotResource: []string{"arn:aws:s3:::other"},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	statements, ok := body["Statement"].([]any)
	if !ok || len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %v", body["Statement"])
	}

	first, ok := statements[0].(map[string]any)
	if !ok {
		t.Fatalf("statement is not an object: %T", statements[0])
	}
	if first["Sid"] != "PrefixAccess" {
		t.Errorf("expected Sid PrefixAccess, got %v", first["Sid"])
	}
	if _, ok := first["NotAction"]; ok {
		t.Errorf("NotAction must be omitted when unset, got %v", first["NotAction"])
	}
	wantCondition := map[string]any{"StringLike": map[string]any{"s3:prefix": []any{"tenant-42/*"}}}
	if !reflect.DeepEqual(first["Condition"], wantCondition) {
		t.Errorf("wrong condition: %v", first["Condition"])
	}

	second, ok := statements[1].(map[string]any)
	if !ok {
		t.Fatalf("statement is not an object: %T", statements[1])
	}
	if _, ok := second["Action"]; ok {
		t.Errorf("Action must be omitted when unset, got %v", second["Action"])
	}
	if _, ok := second["Resource"]; ok {
		t.Errorf("Resource must be omitted when unset, got %v", second["Resource"])
	}
	if !reflect.DeepEqual(second["NotAction"], []any{"sts:AssumeRole"}) {
		t.Errorf("wrong NotAction: %v", second["NotAction"])
	}
	if !reflect.DeepEqual(second["NotResource"], []any{"arn:aws:s3:::other"}) {
		t.Errorf("wrong NotResource: %v", second["NotResource"])
	}
}

func TestPolicyConditionUnmarshalErrors(t *testing.T) {
	tests := map[string]string{
		"nested object value": `{"StringLike": {"s3:prefix": {"nested": "value"}}}`,
		"malformed document":  `{"StringLike": {"s3:prefix": [}`,
		"array of objects":    `{"StringLike": {"s3:prefix": [{"nested": "value"}]}}`,
	}

	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			var condition PolicyCondition
			if err := json.Unmarshal([]byte(document), &condition); err == nil {
				t.Errorf("expected an error for %s", document)
			}
		})
	}
}
