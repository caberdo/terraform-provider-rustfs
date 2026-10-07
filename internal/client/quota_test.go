package client_test

import (
	"strings"
	"testing"
	"time"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
)

func TestReadQuota(t *testing.T) {
	name := randomString()
	dut := getClient()
	name = strings.ToLower(name)
	if err := dut.CreateBucket(name); err != nil {
		t.Fatal(err)
	}
	resp, err := readQuotaWithRetry(t, dut, name)
	if err != nil {
		t.Error(err)
	}
	if resp.Bucket != name {
		t.Error("Bucket readback unexpected value")
	}
	if err := dut.DeleteBucket(name); err != nil {
		t.Fatal(err)
	}
}

func TestCRDQuota(t *testing.T) {
	name := randomString()
	name = strings.ToLower(name)
	quota := client.Quota{
		Bucket: name,
		Quota:  100054541,
	}
	dut := getClient()
	if err := dut.CreateBucket(name); err != nil {
		t.Fatal(err)
	}
	_, err := readQuotaWithRetry(t, dut, name)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	resp, err := dut.SetQuota(quota)
	if err != nil {
		t.Error(err)
	}
	resp, err = readQuotaWithRetry(t, dut, name)
	if err != nil {
		t.Error(err)
	}
	if resp.Quota != quota.Quota {
		t.Error("Readback gave wrong quota")
	}

	if err := dut.DeletQuota(name); err != nil {
		t.Error("error during quota remove")
	}
}

// readQuotaWithRetry tolerates the fresh-server ServiceUnavailable response
// while the scanner computes the bucket's authoritative usage.
func readQuotaWithRetry(t *testing.T, dut client.RustfsAdmin, bucket string) (client.Quota, error) {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		q, err := dut.ReadQuota(bucket)
		if err == nil {
			return q, nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "authoritative bucket usage") {
			return client.Quota{}, err
		}
		time.Sleep(3 * time.Second)
	}
	return client.Quota{}, lastErr
}
