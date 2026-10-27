// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultRetryMaxAttempts is the total number of attempts (one request plus
	// three retries) before a transient answer is surfaced to Terraform.
	defaultRetryMaxAttempts = 4
	// defaultRetryBaseDelay is the first backoff step; it doubles per attempt.
	defaultRetryBaseDelay = 100 * time.Millisecond
	// retryMaxDelay caps one backoff step (the server's Retry-After wins).
	retryMaxDelay = 2 * time.Second
)

// retryableRequest reports whether repeating this request cannot change server
// state. Every admin and direct GET/HEAD route is a read. Mutations are only
// retried when the server defines them as idempotent: a reset, a full-value
// set, or an operation that is a no-op when repeated. Everything else — job
// creation, deletes, credential changes — fails on the first attempt so a
// replay cannot duplicate work.
func retryableRequest(method, relPath string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		return true
	case http.MethodPost:
		switch relPath {
		case "scanner/cycle-state/reset", "scanner/usage-state/reset":
			return true
		case "rebalance/stop":
			// Stopping a stopped rebalance is documented as a no-op.
			return true
		}
	case http.MethodPut:
		if strings.HasPrefix(relPath, "quota/") {
			// A quota PUT carries the whole value, so a replay sets the same
			// limit again.
			return true
		}
		if relPath == "set-config-kv" {
			return true
		}
	}
	return false
}

// retryableStatus lists the answers that describe a transient condition of the
// cluster rather than the request. 403/404/409 and 501 are answers, not
// glitches, and are never retried.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryableErrorCode reports whether the structured server code names a
// transient condition. SlowDown is RustFS's backpressure answer for a busy
// worker or a leader lock; the other two are MinIO-compatible startup answers.
func retryableErrorCode(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case "SlowDown", "RequestTimeout", "XMinioServerNotInitialized":
		return true
	}
	return false
}

// retryableTransportError reports whether a failed round trip is worth
// repeating. A cancelled or expired caller context is the answer, not a
// glitch; every other transport error (connection reset, truncated response)
// may succeed on a fresh connection.
func retryableTransportError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// retryDelay returns how long to wait before the next attempt. A Retry-After
// header wins; otherwise the delay is the base delay doubled per attempt,
// capped, with full jitter in [half, full] so concurrent Terraform runs do not
// retry in lockstep.
func (c *RustfsAdmin) retryDelay(attempt int, res *http.Response) time.Duration {
	if d, ok := retryAfter(res); ok {
		return d
	}
	delay := c.retryBaseDelay
	for i := 0; i < attempt && delay < retryMaxDelay; i++ {
		delay *= 2
	}
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}
	if delay <= 0 {
		return 0
	}
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

// retryAfter reads a Retry-After header, which is either a number of seconds or
// an HTTP date.
func retryAfter(res *http.Response) (time.Duration, bool) {
	if res == nil {
		return 0, false
	}
	value := strings.TrimSpace(res.Header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			seconds = 0
		}
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// sleepRetry waits before the next attempt unless the caller's context ends
// first, in which case the cancellation is the answer.
func sleepRetry(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
