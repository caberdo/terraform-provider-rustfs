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

func TestReadQuota(t *testing.T) {
	name := randomString(8)
	dut := getClient(t)
	name = strings.ToLower(name)
	createBucket(t, dut, name)
	resp, err := dut.ReadQuota(name)
	if err != nil {
		t.Error(err)
	}
	if resp.Bucket != name {
		t.Error("Bucket readback unexpected value")
	}
	deleteBucket(t, dut, name)
}

func TestCRDQuota(t *testing.T) {
	name := randomString(8)
	name = strings.ToLower(name)
	quota := rustfs.Quota{
		Bucket: name,
		Quota:  100054541,
	}
	dut := getClient(t)
	createBucket(t, dut, name)
	_, err := dut.ReadQuota(name)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	resp, err := dut.SetQuota(quota)
	if err != nil {
		t.Error(err)
	}
	resp, err = dut.ReadQuota(name)
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
