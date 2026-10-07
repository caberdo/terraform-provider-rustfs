package provider

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketEncryptionResourceSchema(t *testing.T) {
	r := NewBucketEncryptionResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	if _, ok := attrs["bucket"]; !ok {
		t.Error("expected bucket attribute")
	}
	if _, ok := attrs["algorithm"]; !ok {
		t.Error("expected algorithm attribute")
	}
	if _, ok := attrs["kms_master_key_id"]; !ok {
		t.Error("expected kms_master_key_id attribute")
	}
}

func TestBucketEncryptionResourceMetadata(t *testing.T) {
	r := NewBucketEncryptionResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_encryption" {
		t.Errorf("expected rustfs_bucket_encryption, got %s", resp.TypeName)
	}
}

func TestBuildEncryptionConfig_AES256(t *testing.T) {
	plan := BucketEncryptionResourceModel{
		Bucket:    types.StringValue("test-bucket"),
		Algorithm: types.StringValue("AES256"),
	}
	config := buildEncryptionConfig(plan)

	if len(config.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(config.Rules))
	}
	if config.Rules[0].ApplyServerSideEncryptionByDefault.SSEAlgorithm != "AES256" {
		t.Errorf("expected AES256, got %s", config.Rules[0].ApplyServerSideEncryptionByDefault.SSEAlgorithm)
	}
	if aws.ToString(config.Rules[0].ApplyServerSideEncryptionByDefault.KMSMasterKeyID) != "" {
		t.Error("KMS key should be empty for AES256")
	}
}

func TestBuildEncryptionConfig_AWSKMS(t *testing.T) {
	plan := BucketEncryptionResourceModel{
		Bucket:         types.StringValue("test-bucket"),
		Algorithm:      types.StringValue("aws:kms"),
		KmsMasterKeyID: types.StringValue("arn:aws:kms:us-east-1:123456789012:key/abcd"),
	}
	config := buildEncryptionConfig(plan)

	if aws.ToString(config.Rules[0].ApplyServerSideEncryptionByDefault.KMSMasterKeyID) != "arn:aws:kms:us-east-1:123456789012:key/abcd" {
		t.Errorf("unexpected KMS key ID: %s", aws.ToString(config.Rules[0].ApplyServerSideEncryptionByDefault.KMSMasterKeyID))
	}
}
