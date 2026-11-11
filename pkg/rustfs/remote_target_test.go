// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddRemoteTarget(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if !strings.Contains(r.URL.RawQuery, "bucket=src-bkt") {
			t.Errorf("expected bucket query param, got %s", r.URL.RawQuery)
		}
		if strings.Contains(r.URL.RawQuery, "update=") {
			t.Errorf("create request must not carry update param, got %s", r.URL.RawQuery)
		}
		gotPath = r.URL.Path
		bodyBytes, _ := io.ReadAll(r.Body)
		gotBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`"arn:minio:replication::abc:dst-bkt"`))
	}))
	defer server.Close()
	c := New(&RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(server.URL, "http://"),
	})
	target := RemoteTarget{
		Type:         "replication",
		Endpoint:     "https://peer.example.com",
		Secure:       true,
		TargetBucket: "dst-bkt",
		Credentials: &RemoteTargetCredentials{
			AccessKey: "peeruser",
			SecretKey: "peersecret",
		},
	}
	arn, err := c.AddRemoteTarget(context.Background(), "src-bkt", target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if arn != "arn:minio:replication::abc:dst-bkt" {
		t.Errorf("unexpected arn: %s", arn)
	}
	if !strings.Contains(gotPath, "/set-remote-target") {
		t.Errorf("wrong path: %s", gotPath)
	}
	if !strings.Contains(gotBody, `"type":"replication"`) ||
		!strings.Contains(gotBody, `"targetbucket":"dst-bkt"`) ||
		!strings.Contains(gotBody, `"accessKey":"peeruser"`) {
		t.Errorf("wrong body: %s", gotBody)
	}
}

func TestUpdateRemoteTarget(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`"arn:minio:replication::abc:dst-bkt"`))
	}))
	defer server.Close()
	c := New(&RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(server.URL, "http://"),
	})
	target := RemoteTarget{
		Arn:          "arn:minio:replication::abc:dst-bkt",
		Type:         "replication",
		Endpoint:     "https://peer.example.com",
		Secure:       true,
		TargetBucket: "dst-bkt",
		Credentials: &RemoteTargetCredentials{
			AccessKey: "peeruser",
			SecretKey: "peersecret",
		},
	}
	if _, err := c.AddRemoteTarget(context.Background(), "src-bkt", target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "update=true") {
		t.Errorf("expected update=true, got %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "creds=true") {
		t.Errorf("expected creds=true, got %s", gotQuery)
	}
}

func TestListRemoteTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/list-remote-targets") {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.RawQuery, "bucket=src-bkt") {
			t.Errorf("expected bucket query param, got %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"sourcebucket":"src-bkt","arn":"arn:minio:replication:::peer:backup","type":"replication","endpoint":"https://peer.example.com","secure":true,"targetbucket":"backup"}]`))
	}))
	defer server.Close()
	c := New(&RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(server.URL, "http://"),
	})
	targets, err := c.ListRemoteTargets(context.Background(), "src-bkt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 || targets[0].Arn != "arn:minio:replication:::peer:backup" {
		t.Errorf("unexpected targets: %+v", targets)
	}
}

// TestListRemoteTargetsDeploymentID pins both wire spellings of the
// site-replication peer deployment id. The field is `deployment_id` in
// RustFS-written metadata and `deploymentID` in MinIO/madmin-written metadata,
// and a peer-owned target is recognised by whichever one the server returns.
func TestListRemoteTargetsDeploymentID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"arn":"arn:minio:replication::aaa:dst-a","type":"replication","endpoint":"https://a.example.com","targetbucket":"dst-a","deployment_id":"peer-snake"},
			{"arn":"arn:minio:replication::bbb:dst-b","type":"replication","endpoint":"https://b.example.com","targetbucket":"dst-b","deploymentID":"peer-camel"},
			{"arn":"arn:minio:replication::ccc:dst-c","type":"replication","endpoint":"https://c.example.com","targetbucket":"dst-c"}
		]`))
	}))
	defer server.Close()
	c := New(&RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(server.URL, "http://"),
	})
	targets, err := c.ListRemoteTargets(context.Background(), "src-bkt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}
	for i, want := range []string{"peer-snake", "peer-camel", ""} {
		if got := targets[i].DeploymentID; got != want {
			t.Errorf("target %d deployment id = %q, want %q", i, got, want)
		}
	}
	if got := targets[2].PeerDeploymentID(); got != "" {
		t.Errorf("ordinary target reported peer deployment id %q", got)
	}
}

// TestRemoteTargetPeerOwned covers the two pieces of evidence that make a
// target peer-owned: the deployment id the server records, and the peer segment
// of a target ARN RustFS derived from the site topology. An ARN under the
// ordinary minio partition is not evidence on its own.
func TestRemoteTargetPeerOwned(t *testing.T) {
	cases := []struct {
		name string
		// target is the listing record; the ARN is the only field some cases
		// vary.
		target    RemoteTarget
		wantPeer  string
		wantOwned bool
	}{
		{
			name:      "deployment id marks the target peer-owned",
			target:    RemoteTarget{Arn: "arn:minio:replication::peer-id:dst", DeploymentID: "peer-id"},
			wantPeer:  "peer-id",
			wantOwned: true,
		},
		{
			name:      "topology-derived rustfs ARN marks the target peer-owned",
			target:    RemoteTarget{Arn: "arn:rustfs:replication::peer-from-arn:dst"},
			wantPeer:  "peer-from-arn",
			wantOwned: true,
		},
		{
			name:      "deployment id wins over the ARN peer",
			target:    RemoteTarget{Arn: "arn:rustfs:replication::peer-from-arn:dst", DeploymentID: "peer-id"},
			wantPeer:  "peer-id",
			wantOwned: true,
		},
		{
			name:   "ordinary minio ARN is not peer-owned",
			target: RemoteTarget{Arn: "arn:minio:replication::target-id:dst"},
		},
		{
			name:   "rustfs ARN with an empty peer segment is not peer-owned",
			target: RemoteTarget{Arn: "arn:rustfs:replication:us-east-1::dst"},
		},
		{
			name:   "non-replication ARN is not peer-owned",
			target: RemoteTarget{Arn: "arn:rustfs:s3::peer:dst"},
		},
		{
			name:   "short ARN is not peer-owned",
			target: RemoteTarget{Arn: "arn:rustfs:replication"},
		},
		{
			name:   "empty target is not peer-owned",
			target: RemoteTarget{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.target.PeerDeploymentID(); got != tc.wantPeer {
				t.Errorf("PeerDeploymentID() = %q, want %q", got, tc.wantPeer)
			}
			if got := tc.target.PeerOwned(); got != tc.wantOwned {
				t.Errorf("PeerOwned() = %v, want %v", got, tc.wantOwned)
			}
		})
	}
}

// TestListRemoteTargetsMalformedJSON covers the decode failure: an unreadable
// listing must surface as an error instead of a partial target set.
func TestListRemoteTargetsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"arn":"arn:minio:replication::abc:dst-bkt",`))
	}))
	defer server.Close()
	c := New(&RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(server.URL, "http://"),
	})
	if _, err := c.ListRemoteTargets(context.Background(), "src-bkt"); err == nil {
		t.Fatal("expected a decode error for a truncated listing")
	}
}

func TestDeleteRemoteTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/remove-remote-target") {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.RawQuery, "bucket=src-bkt") ||
			!strings.Contains(r.URL.RawQuery, "arn=arn%3Aminio%3Areplication%3A%3Aabc%3Adst-bkt") {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	c := New(&RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(server.URL, "http://"),
	})
	if err := c.DeleteRemoteTarget(context.Background(), "src-bkt", "arn:minio:replication::abc:dst-bkt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
