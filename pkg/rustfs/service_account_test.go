// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

func getClient(t *testing.T) rustfs.RustfsAdmin {
	t.Helper()
	endpoint := os.Getenv("RUSTFS_ENDPOINT")
	key := os.Getenv("RUSTFS_USER")
	secret := os.Getenv("RUSTFS_SECRET")

	if endpoint == "" || key == "" || secret == "" {
		t.Skip("skipping live RustFS admin test: set RUSTFS_ENDPOINT, RUSTFS_USER and RUSTFS_SECRET to run")
	}

	config := rustfs.RustfsAdminConfig{
		AccessKey:    key,
		AccessSecret: secret,
		Endpoint:     endpoint,

		Ssl: false,
	}

	dut := rustfs.New(&config)
	return dut
}

func randomString(length int) string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)

	for i := range result {
		result[i] = charset[rng.Intn(len(charset))]
	}

	return string(result)
}

// randomServiceAccountName returns a name RustFS accepts: unlike access keys,
// service-account names must start with an ASCII letter.
func randomServiceAccountName() string {
	return "sa" + randomString(8)
}

func TestCreateServiceAccount(t *testing.T) {

	account := rustfs.ServiceAccount{
		AccessKey: randomString(8),
		SecretKey: "someSuperS3cret",
		Name:      randomServiceAccountName(),
	}
	dut := getClient(t)
	err := dut.CreateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
}

func TestCreateAndDeleteServiceAccount(t *testing.T) {

	account := rustfs.ServiceAccount{
		AccessKey: randomString(8),
		SecretKey: "someSuperS3cret",
		Name:      randomServiceAccountName(),
	}
	dut := getClient(t)
	err := dut.CreateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
	err = dut.DeleteServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
}
func TestCreateUpdateAndDeleteServiceAccount(t *testing.T) {

	account := rustfs.ServiceAccount{
		AccessKey: randomString(8),
		SecretKey: "someSuperS3cret",
		Name:      randomServiceAccountName(),
	}
	dut := getClient(t)
	err := dut.CreateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
	account.SecretKey = "insecureOne"
	err = dut.UpdateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
	err = dut.DeleteServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
}
func TestCreateReadAndDeleteServiceAccount(t *testing.T) {

	account := rustfs.ServiceAccount{
		AccessKey: randomString(8),
		SecretKey: "someSuperS3cret",
		Name:      randomServiceAccountName(),
	}
	dut := getClient(t)
	err := dut.CreateServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
	reply, err := dut.ReadServiceAccount(account.AccessKey)
	if err != nil {
		t.Error(err)
	}
	if reply.Name != account.Name {
		t.Error("Read value not matching")
	}
	err = dut.DeleteServiceAccount(account)
	if err != nil {
		t.Error(err)
	}
}

// serviceAccountListPayload is the RustFS list-service-accounts shape: the
// accounts are wrapped in an object.
const serviceAccountListPayload = `{"accounts":[{"parentUser":"u","accountStatus":"on",` +
	`"impliedPolicy":true,"accessKey":"AK","name":"n","description":"d",` +
	`"expiration":"2030-01-01T00:00:00Z"}]}`

func TestListServiceAccounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/list-service-accounts" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(serviceAccountListPayload))
	}))
	defer server.Close()

	client := rustfs.New(&rustfs.RustfsAdminConfig{Endpoint: server.Listener.Addr().String()})
	accounts, err := client.ListServiceAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected one account, got %+v", accounts)
	}
	got := accounts[0]
	if got.AccessKey != "AK" || got.ParentUser != "u" || got.AccountStatus != "on" ||
		got.Name != "n" || got.Description != "d" || !got.ImpliedPolicy {
		t.Errorf("unexpected account: %+v", got)
	}
	if got.SecretKey != "" {
		t.Errorf("secret key must never be returned, got %q", got.SecretKey)
	}
}

// A server regression that returns key material must not put it in the caller's
// hands: the listing blanks SecretKey whatever the payload says.
func TestListServiceAccountsBlanksInjectedSecret(t *testing.T) {
	const payload = `{"accounts":[{"accessKey":"AK","secretKey":"leaked-secret","parentUser":"u"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	client := rustfs.New(&rustfs.RustfsAdminConfig{Endpoint: server.Listener.Addr().String()})
	accounts, err := client.ListServiceAccounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected one account, got %+v", accounts)
	}
	if accounts[0].SecretKey != "" {
		t.Errorf("secret key must be blanked, got %q", accounts[0].SecretKey)
	}
}

func TestListServiceAccountsUnsupportedRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "healthinfo") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version":"refs/tags/1.0.0"}`))
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`))
	}))
	defer server.Close()

	client := rustfs.New(&rustfs.RustfsAdminConfig{Endpoint: server.Listener.Addr().String()})
	_, err := client.ListServiceAccounts()
	if err == nil {
		t.Fatal("expected an error")
	}
	var unsupported *rustfs.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected an UnsupportedFeatureError, got %T: %v", err, err)
	}
	if unsupported.Feature != rustfs.FeatureServiceAccountList {
		t.Errorf("feature = %q", unsupported.Feature)
	}
}

func TestListServiceAccountsFor(t *testing.T) {
	var gotUser string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.URL.Query().Get("user")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(serviceAccountListPayload))
	}))
	defer server.Close()

	client := rustfs.New(&rustfs.RustfsAdminConfig{Endpoint: server.Listener.Addr().String()})
	if _, err := client.ListServiceAccountsFor("parent-user"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotUser != "parent-user" {
		t.Errorf("user query = %q, want parent-user", gotUser)
	}
}
