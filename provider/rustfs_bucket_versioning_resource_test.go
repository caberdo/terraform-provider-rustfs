package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketVersioningResourceSchema(t *testing.T) {
	r := NewBucketVersioningResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"bucket", "status", "mfa_delete", "mfa", "checksum_algorithm", "content_md5", "expected_bucket_owner",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}

	mfa, ok := attrs["mfa"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("mfa attribute is not a StringAttribute")
	}
	if !mfa.Sensitive {
		t.Error("mfa attribute must be Sensitive")
	}

	mfaDelete, ok := attrs["mfa_delete"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("mfa_delete attribute is not a StringAttribute")
	}
	if !mfaDelete.Optional || !mfaDelete.Computed {
		t.Error("mfa_delete must be Optional+Computed")
	}
}

func TestBucketVersioningResourceMetadata(t *testing.T) {
	r := NewBucketVersioningResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_versioning" {
		t.Errorf("expected rustfs_bucket_versioning, got %s", resp.TypeName)
	}
}

func TestBuildVersioningConfiguration(t *testing.T) {
	plan := BucketVersioningResourceModel{
		Bucket:    types.StringValue("test-bucket"),
		Status:    types.StringValue("Enabled"),
		MfaDelete: types.StringValue("Enabled"),
	}
	cfg := buildVersioningConfiguration(plan)
	if cfg.Status != "Enabled" {
		t.Errorf("expected status Enabled, got %s", cfg.Status)
	}
	if cfg.MFADelete != "Enabled" {
		t.Errorf("expected MFADelete Enabled, got %s", cfg.MFADelete)
	}

	plan2 := BucketVersioningResourceModel{
		Bucket:    types.StringValue("test-bucket"),
		Status:    types.StringValue("Suspended"),
		MfaDelete: types.StringNull(),
	}
	cfg2 := buildVersioningConfiguration(plan2)
	if cfg2.MFADelete != "" {
		t.Errorf("expected empty MFADelete when unset, got %s", cfg2.MFADelete)
	}
}

func TestApplyVersioningOutput_EmptyMFADeleteIsDisabled(t *testing.T) {
	model := &BucketVersioningResourceModel{Bucket: types.StringValue("test-bucket")}
	applyVersioningOutput(model, &s3.GetBucketVersioningOutput{
		Status: s3types.BucketVersioningStatusEnabled,
	})

	if got := model.MfaDelete.ValueString(); got != string(s3types.MFADeleteDisabled) {
		t.Errorf("expected MFADelete Disabled when server omits it, got %q", got)
	}
	if got := model.Status.ValueString(); got != string(s3types.BucketVersioningStatusEnabled) {
		t.Errorf("expected status Enabled, got %q", got)
	}
}

func TestApplyVersioningOutput_SetValuesPassThrough(t *testing.T) {
	model := &BucketVersioningResourceModel{Bucket: types.StringValue("test-bucket")}
	applyVersioningOutput(model, &s3.GetBucketVersioningOutput{
		Status:    s3types.BucketVersioningStatusSuspended,
		MFADelete: s3types.MFADeleteStatusEnabled,
	})

	if got := model.MfaDelete.ValueString(); got != string(s3types.MFADeleteStatusEnabled) {
		t.Errorf("expected MFADelete Enabled, got %q", got)
	}
	if got := model.Status.ValueString(); got != string(s3types.BucketVersioningStatusSuspended) {
		t.Errorf("expected status Suspended, got %q", got)
	}
}

func TestApplyVersioningOutput_NilConfig(t *testing.T) {
	model := &BucketVersioningResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Status: types.StringValue("Enabled"),
	}
	applyVersioningOutput(model, nil)
	if got := model.Status.ValueString(); got != "Enabled" {
		t.Errorf("nil config must leave model untouched, got %q", got)
	}
}

func TestIsBucketSubresourceAbsent_Versioning(t *testing.T) {
	if !isBucketSubresourceAbsent(errors.New("NoSuchBucket"), "NoSuchBucket") {
		t.Error("expected NoSuchBucket to be reported absent")
	}
	if isBucketSubresourceAbsent(nil, "NoSuchBucket") {
		t.Error("nil error must not be reported absent")
	}
	if isBucketSubresourceAbsent(errors.New("AccessDenied"), "NoSuchBucket") {
		t.Error("AccessDenied must not be reported absent")
	}
}
