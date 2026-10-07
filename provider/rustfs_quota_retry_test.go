package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

func TestIsTransientQuotaError(t *testing.T) {
	usageTransient := errors.New(`<?xml version="1.0"?><Error><Code>ServiceUnavailable</Code><Message>authoritative bucket usage is not available yet</Message></Error>`)
	if !isTransientQuotaError(usageTransient) {
		t.Fatalf("expected ServiceUnavailable/authoritative usage error to be transient")
	}
	durableTransient := errors.New(`<Error><Code>ServiceUnavailable</Code><Message>durable quota capability is not confirmed across the cluster</Message></Error>`)
	if !isTransientQuotaError(durableTransient) {
		t.Fatalf("expected ServiceUnavailable/durable quota capability error to be transient")
	}
	if isTransientQuotaError(errors.New("ServiceUnavailable without quota readiness message")) {
		t.Fatalf("ServiceUnavailable alone should not be treated as the quota transient error")
	}
	if isTransientQuotaError(errors.New("NoSuchBucket")) {
		t.Fatalf("non-transient error should not be treated as transient")
	}
	if isTransientQuotaError(nil) {
		t.Fatalf("nil error should not be treated as transient")
	}
}

func TestQuotaReadWithRetry(t *testing.T) {
	transientErr := errors.New("<Error><Code>ServiceUnavailable</Code><Message>authoritative bucket usage is not available yet</Message></Error>")

	t.Run("retries transient then succeeds", func(t *testing.T) {
		calls := 0
		read := func(bucket string) (rustfs.Quota, error) {
			calls++
			if calls < 3 {
				return rustfs.Quota{}, transientErr
			}
			return rustfs.Quota{Bucket: bucket, Quota: 100000, Quota_Type: "HARD"}, nil
		}
		got, err := quotaReadWithRetry(context.Background(), "b", read)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Quota != 100000 || calls != 3 {
			t.Fatalf("expected 3 calls and quota 100000, got calls=%d quota=%d", calls, got.Quota)
		}
	})

	t.Run("permanent error fails fast", func(t *testing.T) {
		read := func(bucket string) (rustfs.Quota, error) {
			return rustfs.Quota{}, errors.New("NoSuchBucket")
		}
		start := time.Now()
		_, err := quotaReadWithRetry(context.Background(), "b", read)
		if err == nil || !strings.Contains(err.Error(), "NoSuchBucket") {
			t.Fatalf("expected NoSuchBucket error, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("permanent error should fail fast, took %v", elapsed)
		}
	})

	t.Run("context cancellation stops retries", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		read := func(bucket string) (rustfs.Quota, error) {
			calls++
			cancel()
			return rustfs.Quota{}, transientErr
		}
		_, err := quotaReadWithRetry(ctx, "b", read)
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

func TestQuotaSetWithRetry(t *testing.T) {
	transientErr := errors.New("<Error><Code>ServiceUnavailable</Code><Message>durable quota capability is not confirmed across the cluster</Message></Error>")

	t.Run("retries transient then succeeds", func(t *testing.T) {
		calls := 0
		set := func(q rustfs.Quota) (rustfs.Quota, error) {
			calls++
			if calls < 3 {
				return rustfs.Quota{}, transientErr
			}
			q.Quota_Type = "HARD"
			return q, nil
		}
		got, err := quotaSetWithRetry(context.Background(), rustfs.Quota{Bucket: "b", Quota: 100}, set)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Quota != 100 || calls != 3 {
			t.Fatalf("expected 3 calls and quota 100, got calls=%d quota=%d", calls, got.Quota)
		}
	})

	t.Run("permanent error fails fast", func(t *testing.T) {
		set := func(q rustfs.Quota) (rustfs.Quota, error) {
			return rustfs.Quota{}, errors.New("InvalidArgument")
		}
		start := time.Now()
		_, err := quotaSetWithRetry(context.Background(), rustfs.Quota{Bucket: "b"}, set)
		if err == nil || !strings.Contains(err.Error(), "InvalidArgument") {
			t.Fatalf("expected InvalidArgument error, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("permanent error should fail fast, took %v", elapsed)
		}
	})
}
