package quota

import (
	"context"
	"strings"
	"time"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
)

const (
	// quotaReadMaxAttempts and quotaReadRetryInterval bound the time the quota
	// read tolerates RustFS returning ServiceUnavailable while the scanner is
	// still computing the bucket's authoritative usage on a freshly started
	// server (usually available within tens of seconds).
	quotaReadMaxAttempts   = 30
	quotaReadRetryInterval = 3 * time.Second
)

func isTransientQuotaError(err error) bool {
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

// quotaReadWithRetry retries the quota read while the server reports that the
// bucket's authoritative usage is not computed yet. Other errors fail fast.
func quotaReadWithRetry(ctx context.Context, bucket string, read func(string) (client.Quota, error)) (client.Quota, error) {
	var lastErr error
	for attempt := 0; attempt < quotaReadMaxAttempts; attempt++ {
		quota, err := read(bucket)
		if err == nil {
			return quota, nil
		}
		lastErr = err
		if !isTransientQuotaError(err) {
			return client.Quota{}, err
		}
		timer := time.NewTimer(quotaReadRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return client.Quota{}, ctx.Err()
		case <-timer.C:
		}
	}
	return client.Quota{}, lastErr
}
