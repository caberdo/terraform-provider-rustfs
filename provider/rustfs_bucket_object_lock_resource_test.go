package provider

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketObjectLockResourceSchema(t *testing.T) {
	r := NewBucketObjectLockResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"bucket", "object_lock_enabled", "rule", "checksum_algorithm", "content_md5", "expected_bucket_owner",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}
	if _, ok := attrs["mode"]; ok {
		t.Error("legacy top-level mode attribute must be removed")
	}
	if _, ok := attrs["days"]; ok {
		t.Error("legacy top-level days attribute must be removed")
	}
	if _, ok := attrs["years"]; ok {
		t.Error("legacy top-level years attribute must be removed")
	}

	rule, ok := attrs["rule"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("rule attribute is not a SingleNestedAttribute")
	}
	retention, ok := rule.Attributes["default_retention"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("rule.default_retention is not a SingleNestedAttribute")
	}
	for _, name := range []string{"mode", "days", "years"} {
		if _, ok := retention.Attributes[name]; !ok {
			t.Errorf("expected default_retention.%s attribute", name)
		}
	}
}

func TestBucketObjectLockResourceMetadata(t *testing.T) {
	r := NewBucketObjectLockResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_object_lock" {
		t.Errorf("expected rustfs_bucket_object_lock, got %s", resp.TypeName)
	}
}

func TestBuildObjectLockConfiguration_Days(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue("Enabled"),
		Rule: &BucketObjectLockRuleModel{
			DefaultRetention: &BucketObjectLockDefaultRetentionModel{
				Mode: types.StringValue("GOVERNANCE"),
				Days: types.Int64Value(30),
			},
		},
	}
	cfg, err := buildObjectLockConfiguration(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ObjectLockEnabled != "Enabled" {
		t.Errorf("expected Enabled, got %s", cfg.ObjectLockEnabled)
	}
	if cfg.Rule == nil || cfg.Rule.DefaultRetention == nil {
		t.Fatal("expected default retention")
	}
	if cfg.Rule.DefaultRetention.Mode != "GOVERNANCE" {
		t.Errorf("expected GOVERNANCE, got %s", cfg.Rule.DefaultRetention.Mode)
	}
	if aws.ToInt32(cfg.Rule.DefaultRetention.Days) != 30 {
		t.Errorf("expected 30 days, got %d", aws.ToInt32(cfg.Rule.DefaultRetention.Days))
	}
	if cfg.Rule.DefaultRetention.Years != nil {
		t.Error("years should be nil when only days is set")
	}
}

func TestBuildObjectLockConfiguration_Years(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue("Enabled"),
		Rule: &BucketObjectLockRuleModel{
			DefaultRetention: &BucketObjectLockDefaultRetentionModel{
				Mode:  types.StringValue("COMPLIANCE"),
				Years: types.Int64Value(2),
			},
		},
	}
	cfg, err := buildObjectLockConfiguration(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Rule == nil || cfg.Rule.DefaultRetention == nil {
		t.Fatal("expected default retention")
	}
	if aws.ToInt32(cfg.Rule.DefaultRetention.Years) != 2 {
		t.Errorf("expected 2 years, got %d", aws.ToInt32(cfg.Rule.DefaultRetention.Years))
	}
	if cfg.Rule.DefaultRetention.Days != nil {
		t.Error("days should be nil when only years is set")
	}
}

func TestBuildObjectLockConfiguration_DisabledNoRule(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue(""),
	}
	cfg, err := buildObjectLockConfiguration(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Rule != nil {
		t.Error("expected no rule when not provided")
	}
}

func TestBuildObjectLockConfiguration_RejectsBothDaysAndYears(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue("Enabled"),
		Rule: &BucketObjectLockRuleModel{
			DefaultRetention: &BucketObjectLockDefaultRetentionModel{
				Mode:  types.StringValue("GOVERNANCE"),
				Days:  types.Int64Value(30),
				Years: types.Int64Value(2),
			},
		},
	}
	cfg, err := buildObjectLockConfiguration(plan)
	if err == nil {
		t.Fatal("expected an error when both days and years are set")
	}
	if cfg != nil {
		t.Error("expected nil configuration on error")
	}
}

func TestBuildPutObjectLockConfigurationInput_RejectsBothDaysAndYears(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue("Enabled"),
		Rule: &BucketObjectLockRuleModel{
			DefaultRetention: &BucketObjectLockDefaultRetentionModel{
				Mode:  types.StringValue("COMPLIANCE"),
				Days:  types.Int64Value(1),
				Years: types.Int64Value(1),
			},
		},
	}
	if _, err := buildPutObjectLockConfigurationInput(plan); err == nil {
		t.Fatal("expected an error when both days and years are set")
	}
}

func TestBucketObjectLockResourceSchema_ObjectLockEnabledRejectsDisabled(t *testing.T) {
	r := NewBucketObjectLockResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	attr, ok := resp.Schema.GetAttributes()["object_lock_enabled"].(schema.StringAttribute)
	if !ok {
		t.Fatal("object_lock_enabled is not a StringAttribute")
	}
	if len(attr.Validators) == 0 {
		t.Fatal("object_lock_enabled must carry validators")
	}

	for _, value := range []string{"Disabled", "disabled", ""} {
		req := validator.StringRequest{ConfigValue: types.StringValue(value)}
		vresp := &validator.StringResponse{}
		for _, v := range attr.Validators {
			v.ValidateString(context.TODO(), req, vresp)
		}
		if !vresp.Diagnostics.HasError() {
			t.Errorf("expected object_lock_enabled %q to be rejected", value)
		}
	}

	req := validator.StringRequest{ConfigValue: types.StringValue("Enabled")}
	vresp := &validator.StringResponse{}
	for _, v := range attr.Validators {
		v.ValidateString(context.TODO(), req, vresp)
	}
	if vresp.Diagnostics.HasError() {
		t.Errorf("expected Enabled to be accepted, got: %v", vresp.Diagnostics)
	}
}

func TestBucketObjectLockResourceSchema_DaysYearsValidators(t *testing.T) {
	r := NewBucketObjectLockResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	rule, ok := resp.Schema.GetAttributes()["rule"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule attribute is not a SingleNestedAttribute")
	}
	retention, ok := rule.Attributes["default_retention"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.default_retention is not a SingleNestedAttribute")
	}
	for _, name := range []string{"days", "years"} {
		attr, ok := retention.Attributes[name].(schema.Int64Attribute)
		if !ok {
			t.Fatalf("default_retention.%s is not an Int64Attribute", name)
		}
		if len(attr.Validators) == 0 {
			t.Errorf("default_retention.%s must carry cross-field validators", name)
		}
	}
}
