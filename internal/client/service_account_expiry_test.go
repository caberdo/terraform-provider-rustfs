package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateServiceAccountWithExpirySendsExpirationAndPolicy(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		bodyBytes, _ := io.ReadAll(r.Body)
		gotBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:     server.Listener.Addr().String(),
		AccessKey:    "admin",
		AccessSecret: "secret",
	})

	account := ServiceAccount{
		AccessKey:   "sa-test",
		SecretKey:   "s3cret",
		Name:        "tokens",
		Description: "readonly",
		Expiration:  "2030-01-01T00:00:00Z",
		Policy:      `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::*"]}]}`,
		TargetUser:  "alice",
	}
	if err := client.CreateServiceAccount(account); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "add-service-accounts") {
		t.Errorf("wrong endpoint, path=%s", gotPath)
	}
	for _, want := range []string{`"expiration":"2030-01-01T00:00:00Z"`, `"policy":"{\"Version\":\"2012-10-17\"`, `"impliedPolicy":false`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("missing %s in body: %s", want, gotBody)
		}
	}
}

func TestCreateServiceAccountWithExpiryDefaultsImpliedPolicy(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		gotBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:     server.Listener.Addr().String(),
		AccessKey:    "admin",
		AccessSecret: "secret",
	})

	account := ServiceAccount{
		AccessKey: "sa-test",
		SecretKey: "s3cret",
		Name:      "tokens",
	}
	if err := client.CreateServiceAccount(account); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{`"impliedPolicy":true`, `"expiration":"9999-01-01T00:00:00Z"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("missing %s in body: %s", want, gotBody)
		}
	}
}

func TestUpdateServiceAccountWithExpirySendsNewExpiration(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		gotBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:     server.Listener.Addr().String(),
		AccessKey:    "admin",
		AccessSecret: "secret",
	})

	account := ServiceAccount{
		AccessKey:  "sa-test",
		Expiration: "2031-06-01T00:00:00Z",
	}
	if err := client.UpdateServiceAccount(account); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotBody, `"newExpiration":"2031-06-01T00:00:00Z"`) {
		t.Errorf("expected newExpiration in body: %s", gotBody)
	}
}
