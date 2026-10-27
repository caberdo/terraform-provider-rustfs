// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

var (
	// scannerResetBusyRetries bounds how often a scanner reset is retried
	// while the scanner leader still holds the state lock.
	// scannerResetBusyDelay is a variable so tests can remove the wait.
	scannerResetBusyRetries = 3
	scannerResetBusyDelay   = 2 * time.Second
)

// resetWithScannerLeaderRetry runs a scanner reset call and retries it while
// RustFS reports that the scanner leader still holds the state lock. Both reset
// routes are idempotent, so repeating them is safe; a lock that never frees
// still fails with the server's error.
//
// This is deliberately not the client's retry policy: the leader holds the lock
// for a whole scanner cycle, so the waits are seconds (2s x 3) while the shared
// policy covers transport glitches with sub-second backoff. The reset routes are
// on that policy's allow-list too, so a busy answer may take both.
func resetWithScannerLeaderRetry[T any](ctx context.Context, call func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt <= scannerResetBusyRetries; attempt++ {
		result, err := call()
		if err == nil {
			return result, nil
		}
		if !rustfs.IsScannerLeaderLockBusy(err) {
			return zero, err
		}
		lastErr = err
		if attempt == scannerResetBusyRetries {
			break
		}

		timer := time.NewTimer(scannerResetBusyDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, lastErr
}
