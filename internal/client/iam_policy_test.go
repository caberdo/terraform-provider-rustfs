package client

import (
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

	names, err := client.ListCannedPolicies()
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

	names, err := client.ListCannedPolicies()
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

	names, err := client.ListCannedPolicies()
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

	policy, err := client.ReadPolicy("readwrite")
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
