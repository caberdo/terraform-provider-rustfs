package rustfs_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

func TestAPIErrorParsingXML(t *testing.T) {
	srv := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist</Message></Error>`))
	})

	_, err := srv.ReadQuota("missing")
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "NoSuchBucket" {
		t.Errorf("expected code NoSuchBucket, got %q", apiErr.Code)
	}
	if apiErr.Message != "The specified bucket does not exist" {
		t.Errorf("unexpected message: %q", apiErr.Message)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", apiErr.StatusCode)
	}
	if apiErr.Raw == "" {
		t.Error("expected raw body to be preserved")
	}
	if apiErr.Error() != apiErr.Raw {
		t.Error("Error() must return the raw body for backward compatibility")
	}
	if !rustfs.IsAPIErrorCode(err, "NoSuchBucket") {
		t.Error("IsAPIErrorCode should match NoSuchBucket")
	}
	if rustfs.IsAPIErrorCode(err, "NoSuchKey") {
		t.Error("IsAPIErrorCode must not match unrelated codes")
	}
	if rustfs.IsAPIErrorCode(errors.New("NoSuchBucket"), "NoSuchBucket") {
		t.Error("IsAPIErrorCode must not match plain errors via substring")
	}
}

func TestAPIErrorParsingJSON(t *testing.T) {
	srv := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"internal_error","message":"boom"}`))
	})

	_, err := srv.ReadQuota("x")
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "internal_error" || apiErr.Message != "boom" {
		t.Errorf("unexpected parsed values: code=%q message=%q", apiErr.Code, apiErr.Message)
	}
	if !rustfs.IsAPIErrorCode(err, "internal_error") {
		t.Error("IsAPIErrorCode should match the JSON code")
	}
}

func TestAPIErrorParsingPlainText(t *testing.T) {
	srv := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("access denied"))
	})

	_, err := srv.ReadQuota("x")
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "" {
		t.Errorf("expected empty code, got %q", apiErr.Code)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", apiErr.StatusCode)
	}
	if apiErr.Error() != "access denied" {
		t.Errorf("expected raw body as Error(), got %q", apiErr.Error())
	}
}

func TestAPIErrorViaDirectRequest(t *testing.T) {
	srv := newTestAdminServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchConfiguration</Code><Message>absent</Message></Error>`))
	})

	resp, err := srv.DoDirectRequest(context.Background(), rustfs.RequestData{Method: "GET", RelPath: "thing"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "NoSuchConfiguration" {
		t.Errorf("expected code NoSuchConfiguration, got %q", apiErr.Code)
	}
	if !rustfs.IsAPIErrorCode(err, "NoSuchConfiguration") {
		t.Error("IsAPIErrorCode should match via DoDirectRequest")
	}
}
