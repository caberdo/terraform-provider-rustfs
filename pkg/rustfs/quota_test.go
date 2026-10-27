// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

// createBucket is a test-local helper that creates a bucket through the S3
// data plane (the admin client no longer ships bucket create/delete helpers).
func createBucket(t *testing.T, dut rustfs.RustfsAdmin, bucket string) {
	t.Helper()
	req := rustfs.RequestData{Method: "PUT", RelPath: strings.ToLower(bucket)}
	resp, err := dut.DoDirectRequest(context.Background(), req)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
}

func deleteBucket(t *testing.T, dut rustfs.RustfsAdmin, bucket string) {
	t.Helper()
	req := rustfs.RequestData{Method: "DELETE", RelPath: strings.ToLower(bucket)}
	resp, err := dut.DoDirectRequest(context.Background(), req)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
}

// quotaColdStartDeadline bounds how long the live tests tolerate RustFS
// reporting that a bucket's authoritative usage is not computed yet on a
// freshly started server. It mirrors the provider's quotaReadWithRetry budget
// (30 attempts every 3s), so the live tests fail only where the provider would.
const (
	quotaColdStartDeadline = 90 * time.Second
	quotaColdStartInterval = 3 * time.Second
)

// isQuotaColdStart reports whether err is the documented fresh-server response
// that the provider's quota retry absorbs: usage not computed yet, or the
// durable quota capability not confirmed.
func isQuotaColdStart(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if !strings.Contains(msg, "ServiceUnavailable") {
		return false
	}
	return strings.Contains(msg, "authoritative bucket usage") ||
		strings.Contains(msg, "durable quota capability is not confirmed")
}

// retryQuotaColdStart runs op until it succeeds, fails with an error that is
// not a cold-start response, or the deadline expires. Failures report the last
// underlying error instead of masking it behind a timeout.
func retryQuotaColdStart(t *testing.T, op func() error) {
	t.Helper()
	deadline := time.Now().Add(quotaColdStartDeadline)
	for {
		err := op()
		if err == nil {
			return
		}
		if !isQuotaColdStart(err) {
			t.Fatalf("quota operation failed: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("quota still not authoritative after %s: %v", quotaColdStartDeadline, err)
		}
		t.Logf("quota not authoritative yet, retrying in %s: %v", quotaColdStartInterval, err)
		time.Sleep(quotaColdStartInterval)
	}
}

func readQuotaEventually(t *testing.T, dut rustfs.RustfsAdmin, bucket string) rustfs.Quota {
	t.Helper()
	var quota rustfs.Quota
	retryQuotaColdStart(t, func() error {
		var err error
		quota, err = dut.ReadQuota(context.Background(), bucket)
		return err
	})
	return quota
}

func setQuotaEventually(t *testing.T, dut rustfs.RustfsAdmin, quota rustfs.Quota) rustfs.Quota {
	t.Helper()
	var got rustfs.Quota
	retryQuotaColdStart(t, func() error {
		var err error
		got, err = dut.SetQuota(context.Background(), quota)
		return err
	})
	return got
}

func TestReadQuota(t *testing.T) {
	name := strings.ToLower(randomString(8))
	dut := getClient(t)
	createBucket(t, dut, name)
	t.Cleanup(func() { deleteBucket(t, dut, name) })

	resp := readQuotaEventually(t, dut, name)
	if resp.Bucket != name {
		t.Error("Bucket readback unexpected value")
	}
}

func TestCRDQuota(t *testing.T) {
	name := strings.ToLower(randomString(8))
	quota := rustfs.Quota{
		Bucket: name,
		Quota:  100054541,
	}
	dut := getClient(t)
	createBucket(t, dut, name)
	t.Cleanup(func() { deleteBucket(t, dut, name) })

	readQuotaEventually(t, dut, name)
	setQuotaEventually(t, dut, quota)
	resp := readQuotaEventually(t, dut, name)
	if resp.Quota != quota.Quota {
		t.Error("Readback gave wrong quota")
	}

	if err := dut.DeletQuota(context.Background(), name); err != nil {
		t.Error("error during quota remove")
	}
}
