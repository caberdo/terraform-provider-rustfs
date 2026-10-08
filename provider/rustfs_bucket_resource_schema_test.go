package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketResourceSchema(t *testing.T) {
	r := NewBucketResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"name",
		"acl",
		"bucket_namespace",
		"object_ownership",
		"object_lock_enabled_for_bucket",
		"grant_read",
		"grant_write",
		"grant_read_acp",
		"grant_write_acp",
		"grant_full_control",
		"create_bucket_configuration",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %q attribute", name)
		}
	}
}

func TestBucketResourceMetadata(t *testing.T) {
	r := NewBucketResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket" {
		t.Errorf("expected rustfs_bucket, got %s", resp.TypeName)
	}
}

func TestBuildCreateBucketConfiguration(t *testing.T) {
	tags, diags := types.MapValueFrom(context.TODO(), types.StringType, map[string]string{
		"env":  "test",
		"team": "platform",
	})
	if diags.HasError() {
		t.Fatalf("map value: %v", diags)
	}

	cfg := &createBucketConfigurationModel{
		LocationConstraint: types.StringValue("eu-west-1"),
		Tags:               tags,
	}

	out, cfgDiags := buildCreateBucketConfiguration(context.TODO(), cfg)
	if cfgDiags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", cfgDiags)
	}
	if out == nil {
		t.Fatal("expected configuration")
	}
	if string(out.LocationConstraint) != "eu-west-1" {
		t.Errorf("expected eu-west-1, got %s", out.LocationConstraint)
	}
	if len(out.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(out.Tags))
	}
	// Tags are emitted in sorted key order for determinism.
	if len(out.Tags) == 2 && *out.Tags[0].Key != "env" {
		t.Errorf("expected first tag key env, got %s", *out.Tags[0].Key)
	}
}

func TestBuildCreateBucketConfigurationEmpty(t *testing.T) {
	cfg := &createBucketConfigurationModel{
		LocationConstraint: types.StringNull(),
		Tags:               types.MapNull(types.StringType),
	}
	out, cfgDiags := buildCreateBucketConfiguration(context.TODO(), cfg)
	if cfgDiags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", cfgDiags)
	}
	if out == nil {
		t.Fatal("expected non-nil configuration")
	}
	if out.LocationConstraint != "" {
		t.Errorf("expected empty location constraint, got %s", out.LocationConstraint)
	}
	if len(out.Tags) != 0 {
		t.Errorf("expected no tags, got %d", len(out.Tags))
	}
}

func TestIsBucketNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "typed NotFound",
			err:  &s3types.NotFound{},
			want: true,
		},
		{
			name: "typed NoSuchBucket",
			err:  &s3types.NoSuchBucket{},
			want: true,
		},
		{
			name: "wrapped typed NotFound",
			err:  fmt.Errorf("head bucket failed: %w", &s3types.NotFound{}),
			want: true,
		},
		{
			name: "wrapped typed NoSuchBucket",
			err:  fmt.Errorf("delete bucket failed: %w", &s3types.NoSuchBucket{}),
			want: true,
		},
		{
			name: "access denied mentioning 404 in bucket name",
			err:  errors.New("access denied for bucket app-404-logs"),
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("connection reset by peer"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBucketNotFound(tt.err); got != tt.want {
				t.Fatalf("isBucketNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
