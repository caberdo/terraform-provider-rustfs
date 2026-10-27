// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Both API clients must identify themselves with the provider version, so a
// server operator can tell provider traffic from mc and match a bug report to a
// build. The admin client sets the header directly; the S3 client carries the
// same token through the AWS SDK's AppID.
func TestProviderClientsIdentifyWithVersion(t *testing.T) {
	var adminUA, s3UA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/rustfs/admin/") {
			adminUA = r.Header.Get("User-Agent")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		s3UA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("RUSTFS_ENDPOINT", "")
	t.Setenv("RUSTFS_USER", "")
	t.Setenv("RUSTFS_SECRET", "")

	resp := configureProvider(t, RustfsProviderModel{
		Endpoint:  types.StringValue(srv.URL),
		AccessKey: types.StringValue(configuredAccessKey),
		SecretKey: types.StringValue(configuredSecretKey),
	})
	requireNoDiagnostics(t, resp.Diagnostics)

	client, ok := resp.ResourceData.(*AllClient)
	if !ok {
		t.Fatalf("expected *AllClient in ResourceData, got %T", resp.ResourceData)
	}

	if _, err := client.RustClient.GetKmsStatus(context.Background()); err != nil {
		t.Fatalf("admin call: %v", err)
	}
	if _, err := client.S3.HeadBucket(context.Background(), &s3.HeadBucketInput{Bucket: aws.String("bucket")}); err != nil {
		t.Fatalf("s3 call: %v", err)
	}

	if !strings.Contains(adminUA, "terraform-provider-rustfs/test") {
		t.Fatalf("admin User-Agent = %q, want the provider version token", adminUA)
	}
	if !strings.Contains(s3UA, "terraform-provider-rustfs/test") {
		t.Fatalf("s3 User-Agent = %q, want the provider version token", s3UA)
	}
}
