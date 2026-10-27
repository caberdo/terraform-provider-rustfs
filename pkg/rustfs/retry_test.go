// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testRetryClient(t *testing.T, url string) RustfsAdmin {
	t.Helper()
	return New(&RustfsAdminConfig{
		AccessKey:        "admin",
		AccessSecret:     "secret",
		Endpoint:         strings.TrimPrefix(url, "http://"),
		RetryBaseDelay:   time.Millisecond,
		RetryMaxAttempts: defaultRetryMaxAttempts,
	})
}

func TestRetryableRequest(t *testing.T) {
	tests := []struct {
		method, relPath string
		want            bool
	}{
		{"GET", "kms/status", true},
		{"HEAD", "kms/status", true},
		{"GET", "export-iam", true},
		{"POST", "scanner/cycle-state/reset", true},
		{"POST", "scanner/usage-state/reset", true},
		{"POST", "rebalance/stop", true},
		{"PUT", "quota/bucket", true},
		{"PUT", "set-config-kv", true},
		{"POST", "kms/keys", false},
		{"POST", "integrity/logs/jobs", false},
		{"POST", "integrity/logs/jobs/1/control", false},
		{"POST", "tier/tier-1", false},
		{"POST", "rebalance/start", false},
		{"PUT", "add-user", false},
		{"PUT", "site-replication/repair", false},
		{"DELETE", "quota/bucket", false},
		{"DELETE", "clear-config-history-kv", false},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.relPath, func(t *testing.T) {
			if got := retryableRequest(tt.method, tt.relPath); got != tt.want {
				t.Fatalf("retryableRequest(%q, %q) = %v, want %v", tt.method, tt.relPath, got, tt.want)
			}
		})
	}
}

func TestRetryableStatus(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 504} {
		if !retryableStatus(status) {
			t.Fatalf("status %d should be retryable", status)
		}
	}
	for _, status := range []int{200, 204, 400, 401, 403, 404, 409, 501, 511} {
		if retryableStatus(status) {
			t.Fatalf("status %d must not be retried", status)
		}
	}
}

func TestRetryableErrorCode(t *testing.T) {
	retryable := []string{"SlowDown", "RequestTimeout", "XMinioServerNotInitialized"}
	for _, code := range retryable {
		if !retryableErrorCode(&APIError{Code: code, StatusCode: 500}) {
			t.Fatalf("code %q should be retryable", code)
		}
	}
	for _, code := range []string{"NoSuchKey", "NoSuchBucket", "AccessDenied", "InternalError"} {
		if retryableErrorCode(&APIError{Code: code, StatusCode: 500}) {
			t.Fatalf("code %q must not be retried", code)
		}
	}
	if retryableErrorCode(errors.New("plain error")) {
		t.Fatal("a non-APIError must not be classified by code")
	}
}

func TestRetryAfterHeader(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"2", 2 * time.Second, true},
		{"0", 0, true},
		{"-3", 0, true},
		{"", 0, false},
		{"not-a-delay", 0, false},
	}
	for _, tt := range tests {
		res := &http.Response{Header: http.Header{}}
		if tt.value != "" {
			res.Header.Set("Retry-After", tt.value)
		}
		got, ok := retryAfter(res)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("retryAfter(%q) = %v, %v; want %v, %v", tt.value, got, ok, tt.want, tt.ok)
		}
	}

	date := time.Now().Add(90 * time.Minute).UTC().Format(http.TimeFormat)
	res := &http.Response{Header: http.Header{}}
	res.Header.Set("Retry-After", date)
	got, ok := retryAfter(res)
	if !ok || got < 89*time.Minute {
		t.Fatalf("retryAfter(date) = %v, %v; want about 90m", got, ok)
	}

	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	res.Header.Set("Retry-After", past)
	got, ok = retryAfter(res)
	if !ok || got != 0 {
		t.Fatalf("retryAfter(past date) = %v, %v; want 0, true", got, ok)
	}
}

func TestRetryableTransportError(t *testing.T) {
	if retryableTransportError(nil) {
		t.Fatal("nil is not a transport error")
	}
	if retryableTransportError(context.Canceled) || retryableTransportError(context.DeadlineExceeded) {
		t.Fatal("a cancelled or expired context is an answer, not a glitch")
	}
	if !retryableTransportError(errors.New("connection reset by peer")) {
		t.Fatal("a broken round trip should be retried")
	}
}

func TestRetryDelayIsBoundedAndHonoursRetryAfter(t *testing.T) {
	client := New(&RustfsAdminConfig{
		Endpoint:       "host:9000",
		RetryBaseDelay: 100 * time.Millisecond,
	})
	if got := client.retryDelay(0, nil); got < 50*time.Millisecond || got > 100*time.Millisecond {
		t.Fatalf("retryDelay(0) = %v, want a jittered 50-100ms", got)
	}
	if got := client.retryDelay(30, nil); got < retryMaxDelay/2 || got > retryMaxDelay {
		t.Fatalf("retryDelay(30) = %v, want the %v cap", got, retryMaxDelay)
	}
	res := &http.Response{Header: http.Header{}}
	res.Header.Set("Retry-After", "7")
	if got := client.retryDelay(0, res); got != 7*time.Second {
		t.Fatalf("retryDelay with Retry-After = %v, want 7s", got)
	}
	client.retryBaseDelay = 0
	if got := client.retryDelay(3, nil); got != 0 {
		t.Fatalf("retryDelay without a base delay = %v, want 0", got)
	}
}

func TestSleepRetry(t *testing.T) {
	if err := sleepRetry(context.Background(), 0); err != nil {
		t.Fatalf("a zero delay should not sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepRetry(ctx, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepRetry with a cancelled context = %v, want context.Canceled", err)
	}
	if err := sleepRetry(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequestRetriesTransientAnswer(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`<Error><Code>SlowDown</Code><Message>retry</Message></Error>`))
			return
		}
		_, _ = w.Write([]byte(`{"backend":"local"}`))
	}))
	defer server.Close()

	client := testRetryClient(t, server.URL)
	status, err := client.GetKmsStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if status == nil {
		t.Fatal("expected a decoded status")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("server calls = %d, want 3", got)
	}
}

func TestDoRequestRetriesAllowListedMutation(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := testRetryClient(t, server.URL)
	if _, err := client.ResetScannerCycleState(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("server calls = %d, want 2", got)
	}
}

func TestDoRequestDoesNotRetryUnlistedMutation(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := testRetryClient(t, server.URL)
	_, err := client.CreateKmsKey(context.Background(), "key")
	if err == nil {
		t.Fatal("expected a service-unavailable error")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("server calls = %d, want 1: a create must not be replayed", got)
	}
}

func TestDoRequestDoesNotRetryPermanentAnswer(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`))
	}))
	defer server.Close()

	client := testRetryClient(t, server.URL)
	_, err := client.GetKmsStatus(context.Background())
	if !IsAPIErrorCode(err, "AccessDenied") {
		t.Fatalf("expected AccessDenied, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("server calls = %d, want 1", got)
	}
}

func TestDoRequestReturnsLastTransientErrorAfterAttempts(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`<Error><Code>ServiceUnavailable</Code><Message>still starting</Message></Error>`))
	}))
	defer server.Close()

	client := testRetryClient(t, server.URL)
	_, err := client.GetKmsStatus(context.Background())
	if !IsAPIErrorCode(err, "ServiceUnavailable") {
		t.Fatalf("expected the server's last error, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != defaultRetryMaxAttempts {
		t.Fatalf("server calls = %d, want %d", got, defaultRetryMaxAttempts)
	}
}

func TestDoRequestCancellationStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := testRetryClient(t, server.URL)
	client.retryBaseDelay = time.Hour

	_, err := client.GetKmsStatus(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("server calls = %d, want 1", got)
	}
}

func TestDoRequestWritesUserAgent(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"backend":"local"}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		AccessKey:        "admin",
		AccessSecret:     "secret",
		Endpoint:         strings.TrimPrefix(server.URL, "http://"),
		UserAgent:        "terraform-provider-rustfs/9.9.9",
		RetryMaxAttempts: 1,
	})
	if _, err := client.GetKmsConfig(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != "terraform-provider-rustfs/9.9.9" {
		t.Fatalf("User-Agent = %q, want the configured value", seen)
	}
}

func TestUserAgentForVersion(t *testing.T) {
	got := UserAgentForVersion("1.2.3")
	if !strings.HasPrefix(got, "terraform-provider-rustfs/1.2.3 (") {
		t.Fatalf("UserAgentForVersion = %q, want the version token", got)
	}
	if !strings.Contains(got, "go") || !strings.Contains(got, "/") {
		t.Fatalf("UserAgentForVersion = %q, want the Go version and OS/arch", got)
	}
	if dev := UserAgentForVersion(""); !strings.HasPrefix(dev, "terraform-provider-rustfs/dev (") {
		t.Fatalf("UserAgentForVersion(\"\") = %q, want the dev identifier", dev)
	}
}

func TestDoDirectRequestRetriesTransientAnswer(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := testRetryClient(t, server.URL)
	res, err := client.DoDirectRequest(context.Background(), RequestData{Method: http.MethodGet, RelPath: "health"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	drainClose(res)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("server calls = %d, want 2", got)
	}
}

func TestDoRequestRetriesTransportError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"backend":"local"}`))
	}))
	defer server.Close()

	client := testRetryClient(t, server.URL)
	status, err := client.GetKmsStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error after a broken connection: %v", err)
	}
	if status == nil || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("status = %v, calls = %d; want a retried request", status, atomic.LoadInt32(&calls))
	}
}

func TestSleepRetryStopsOnCancellationDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	if err := sleepRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepRetry = %v, want context.Canceled during the wait", err)
	}
}

func TestDoRequestCancellationStopsRetryBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	go func() {
		// Cancel once the first answer has certainly been processed: the
		// client is then sleeping in the backoff, not blocked on the request.
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	client := testRetryClient(t, server.URL)
	client.retryBaseDelay = time.Hour
	_, err := client.GetKmsStatus(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestDoRequestCancellationStopsTransportBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	client := testRetryClient(t, server.URL)
	client.retryBaseDelay = time.Hour
	_, err := client.GetKmsStatus(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
