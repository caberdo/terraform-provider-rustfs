// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestAddTier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !json.Valid(body) {
			t.Errorf("expected valid JSON body, got %s", string(body))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	config := json.RawMessage(`{"tier_type":"s3","s3":{"name":"MYTIER"}}`)
	err := client.AddTier(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveTier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	err := client.RemoveTier("MYTIER")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListTiers(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    []TierInfo
		wantErr bool
	}{
		{
			// The server nests each backend under its type and leaves the
			// enclosing tier name out; the backend's own name identifies it.
			name:   "lists names and types",
			status: http.StatusOK,
			body:   `[{"type":"rustfs","rustfs":{"name":"COLD","endpoint":"https://cold.example.org"}},{"type":"s3","s3":{"name":"ARCHIVE"}}]`,
			want: []TierInfo{
				{Name: "ARCHIVE", TierType: "s3", Config: `{"name":"ARCHIVE"}`},
				{Name: "COLD", TierType: "rustfs", Config: `{"endpoint":"https://cold.example.org","name":"COLD"}`},
			},
		},
		{
			// The provider redacts every known credential key on its own side,
			// including one nested in a sub-object, and keeps the rest of the
			// backend intact.
			name:   "redacts credentials in the backend config",
			status: http.StatusOK,
			body: `[{"type":"s3","s3":{"name":"COLD","access_key":"AK","secret_key":"super-secret",` +
				`"session_token":"session","extra":{"clientSecret":"nested-secret","region":"eu-west-1"},` +
				`"account_key":""}}]`,
			want: []TierInfo{{
				Name:     "COLD",
				TierType: "s3",
				Config: `{"access_key":"AK","account_key":"","extra":{"clientSecret":"REDACTED","region":"eu-west-1"},` +
					`"name":"COLD","secret_key":"REDACTED","session_token":"REDACTED"}`,
			}},
		},
		{
			name:   "no tiers",
			status: http.StatusOK,
			body:   `[]`,
			want:   []TierInfo{},
		},
		{
			name:   "entry without a backend block",
			status: http.StatusOK,
			body:   `[{"type":"rustfs"}]`,
			want:   []TierInfo{},
		},
		{
			name:   "entry without a type",
			status: http.StatusOK,
			body:   `[{"rustfs":{"name":"COLD"}}]`,
			want:   []TierInfo{},
		},
		{
			name:    "malformed body",
			status:  http.StatusOK,
			body:    `not json`,
			wantErr: true,
		},
		{
			// One unusable entry must not hide the usable ones.
			name:   "malformed entries are skipped",
			status: http.StatusOK,
			body:   `[{"type":{"nested":true}},{"type":"rustfs","rustfs":"not-an-object"},{"type":"s3","s3":{"name":"ARCHIVE"}}]`,
			want:   []TierInfo{{Name: "ARCHIVE", TierType: "s3", Config: `{"name":"ARCHIVE"}`}},
		},
		{
			name:    "server failure",
			status:  http.StatusForbidden,
			body:    `<Error><Code>AccessDenied</Code><Message>nope</Message></Error>`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("expected GET, got %s", r.Method)
				}
				if r.URL.Path != "/rustfs/admin/v3/tier" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client := New(&RustfsAdminConfig{
				Endpoint: server.Listener.Addr().String(),
			})

			got, err := client.ListTiers()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("ListTiers = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTierExists(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantExists bool
		wantErr    bool
	}{
		{
			// Probed on 1.0.1 and latest: a configured tier answers 200.
			name:       "configured tier answers 200",
			status:     http.StatusOK,
			wantExists: true,
		},
		{
			// The route reports a missing tier as a 500 with TierNotFound,
			// not as a 404, so the code is what decides.
			name:   "missing tier answers TierNotFound",
			status: http.StatusInternalServerError,
			body:   `<Error><Code>TierNotFound</Code><Message>tier not found!</Message></Error>`,
		},
		{
			name:   "NoSuchTier is treated the same way",
			status: http.StatusNotFound,
			body:   `<Error><Code>NoSuchTier</Code><Message>no such tier</Message></Error>`,
		},
		{
			name:    "other failures are returned",
			status:  http.StatusForbidden,
			body:    `<Error><Code>AccessDenied</Code><Message>nope</Message></Error>`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("expected GET, got %s", r.Method)
				}
				if r.URL.Path != "/rustfs/admin/v3/tier/MYTIER" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			// No credentials: the mocked server never verifies the signature,
			// so no credential-shaped value belongs in the source.
			client := New(&RustfsAdminConfig{
				Endpoint: server.Listener.Addr().String(),
			})

			exists, err := client.TierExists("MYTIER")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exists != tc.wantExists {
				t.Errorf("TierExists = %v, want %v", exists, tc.wantExists)
			}
		})
	}
}

func TestListTiersCapabilityError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"not implemented"}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{Endpoint: server.Listener.Addr().String()})

	if _, err := client.ListTiers(); !IsUnsupportedFeature(err) {
		t.Fatalf("expected an unsupported-feature error, got %v", err)
	}
}

func TestRedactTierSecretsCoversArraysAndMarshalFailure(t *testing.T) {
	encoded := redactTierSecrets(map[string]any{
		"targets": []any{
			map[string]any{"secretKey": "nested-secret", "accessKey": "AK"},
		},
	})
	if encoded == "" {
		t.Fatal("expected the array value to be encoded")
	}
	if !containsAll(encoded, `"secretKey":"REDACTED"`, `"accessKey":"AK"`) {
		t.Errorf("array credential was not redacted: %s", encoded)
	}

	if encoded := redactTierSecrets(make(chan int)); encoded != "" {
		t.Errorf("an unencodable value must yield an empty config, got %q", encoded)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
