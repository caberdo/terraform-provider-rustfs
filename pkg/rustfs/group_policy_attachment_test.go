// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newAssociationServer returns a test server that records every policy
// association request it receives and answers 200 OK.
func newAssociationServer(t *testing.T) (*httptest.Server, func() []policyAssociationRequest, func() string) {
	t.Helper()

	var mu sync.Mutex
	var requests []policyAssociationRequest
	var methods, paths []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body policyAssociationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		mu.Lock()
		requests = append(requests, body)
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	return server,
		func() []policyAssociationRequest {
			mu.Lock()
			defer mu.Unlock()
			return append([]policyAssociationRequest(nil), requests...)
		},
		func() string {
			mu.Lock()
			defer mu.Unlock()
			return strings.Join(methods, " ") + " " + strings.Join(paths, " ")
		}
}

func newAssociationClient(t *testing.T, endpoint string) *RustfsAdmin {
	t.Helper()

	client := New(&RustfsAdminConfig{
		Endpoint:  endpoint,
		AccessKey: "admin",
	})
	client.accessSecret = "secret"
	return &client
}

func TestAttachGroupPolicy(t *testing.T) {
	server, requests, methodPath := newAssociationServer(t)
	client := newAssociationClient(t, server.Listener.Addr().String())

	if err := client.AttachGroupPolicy(context.Background(), "developers", "readwrite"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := methodPath(); !strings.HasPrefix(got, "POST ") || !strings.Contains(got, "/idp/builtin/policy/attach") {
		t.Errorf("expected POST to the attach endpoint, got %s", got)
	}
	if got := methodPath(); strings.Contains(got, "set-user-or-group-policy") {
		t.Errorf("attach must not use the replace-style endpoint, got %s", got)
	}
	got := requests()
	if len(got) != 1 {
		t.Fatalf("expected 1 request, got %d", len(got))
	}
	if got[0].Group != "developers" {
		t.Errorf("expected group=developers, got %q", got[0].Group)
	}
	if got[0].User != "" {
		t.Errorf("expected no user in a group request, got %q", got[0].User)
	}
	if len(got[0].Policies) != 1 || got[0].Policies[0] != "readwrite" {
		t.Errorf("expected policies=[readwrite], got %v", got[0].Policies)
	}
}

func TestDetachGroupPolicy(t *testing.T) {
	server, requests, methodPath := newAssociationServer(t)
	client := newAssociationClient(t, server.Listener.Addr().String())

	if err := client.DetachGroupPolicy(context.Background(), "developers", "readwrite"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := methodPath(); !strings.HasPrefix(got, "POST ") || !strings.Contains(got, "/idp/builtin/policy/detach") {
		t.Errorf("expected POST to the detach endpoint, got %s", got)
	}
	if got := methodPath(); strings.Contains(got, "set-user-or-group-policy") {
		t.Errorf("detach must not use the replace-style endpoint, got %s", got)
	}
	got := requests()
	if len(got) != 1 {
		t.Fatalf("expected 1 request, got %d", len(got))
	}
	if got[0].Group != "developers" {
		t.Errorf("expected group=developers, got %q", got[0].Group)
	}
	// The named policy must be sent: an empty policy list is rejected by the
	// admin API and used to clear the group's whole mapping.
	if len(got[0].Policies) != 1 || got[0].Policies[0] != "readwrite" {
		t.Errorf("expected policies=[readwrite], got %v", got[0].Policies)
	}
}

func TestGroupPolicyAssociationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := newAssociationClient(t, server.Listener.Addr().String())

	if err := client.AttachGroupPolicy(context.Background(), "developers", "readwrite"); err == nil {
		t.Fatal("expected error for non-2xx response, got nil")
	}
	if err := client.DetachGroupPolicy(context.Background(), "developers", "readwrite"); err == nil {
		t.Fatal("expected error for non-2xx response, got nil")
	}
}

// A missing group is reported as NoSuchResource; the resource's Read and
// Delete rely on that code to drop a dangling attachment and to treat a
// destroy after the group was removed as a no-op.
func TestGroupPolicyAssociationMissingGroup(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchResource</Code><Message>group 'gone' does not exist</Message></Error>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	client := newAssociationClient(t, server.Listener.Addr().String())

	if err := client.DetachGroupPolicy(context.Background(), "gone", "readwrite"); !IsAPIErrorCode(err, "NoSuchResource") {
		t.Errorf("expected a NoSuchResource error from detach, got %v", err)
	}
	if _, err := client.GetGroup(context.Background(), "gone"); !IsAPIErrorCode(err, "NoSuchResource") {
		t.Errorf("expected a NoSuchResource error from the group read, got %v", err)
	}
}

// TestGroupPolicyAssociationIsSerialized pins the serialization the additive
// endpoints need: they read the target's policy list, modify it and write it
// back, so two in-flight requests for the same group can drop a policy.
func TestGroupPolicyAssociationIsSerialized(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := inFlight.Add(1)
		for {
			max := maxInFlight.Load()
			if current <= max || maxInFlight.CompareAndSwap(max, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newAssociationClient(t, server.Listener.Addr().String())

	var wg sync.WaitGroup
	for _, policy := range []string{"readwrite", "diagnostics"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := client.AttachGroupPolicy(context.Background(), "developers", policy); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("expected the group policy association calls to be serialized, saw %d in flight", got)
	}
}
