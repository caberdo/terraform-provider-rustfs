// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestBucketLifecycleConfigurationSchema(t *testing.T) {
	r := NewBucketLifecycleConfigurationResource()
	resp := frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), frameworkresource.SchemaRequest{}, &resp)

	if _, ok := resp.Schema.Attributes["bucket"]; !ok {
		t.Error("missing bucket attribute")
	}
	if _, ok := resp.Schema.Attributes["id"]; !ok {
		t.Error("missing id attribute")
	}
	if _, ok := resp.Schema.Attributes["transition_default_minimum_object_size"]; !ok {
		t.Error("missing transition_default_minimum_object_size attribute")
	}

	ruleAttr, ok := resp.Schema.Attributes["rule"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatal("rule must be a schema.ListNestedAttribute")
	}

	for _, want := range []string{
		"id", "status", "filter", "expiration", "transition",
		"noncurrent_version_expiration", "noncurrent_version_transition",
		"abort_incomplete_multipart_upload",
	} {
		if _, ok := ruleAttr.NestedObject.Attributes[want]; !ok {
			t.Errorf("missing rule attribute %q", want)
		}
	}

	filter, ok := ruleAttr.NestedObject.Attributes["filter"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("filter must be a schema.SingleNestedAttribute")
	}
	for _, want := range []string{"prefix", "tag", "object_size_greater_than", "object_size_less_than", "and"} {
		if _, ok := filter.Attributes[want]; !ok {
			t.Errorf("missing filter attribute %q", want)
		}
	}

	and, ok := filter.Attributes["and"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("filter.and must be a schema.SingleNestedAttribute")
	}
	for _, want := range []string{"prefix", "tags", "object_size_greater_than", "object_size_less_than"} {
		if _, ok := and.Attributes[want]; !ok {
			t.Errorf("missing filter.and attribute %q", want)
		}
	}

	expiration, ok := ruleAttr.NestedObject.Attributes["expiration"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("expiration must be a schema.SingleNestedAttribute")
	}
	for _, want := range []string{"days", "date", "expired_object_delete_marker"} {
		if _, ok := expiration.Attributes[want]; !ok {
			t.Errorf("missing expiration attribute %q", want)
		}
	}

	transition, ok := ruleAttr.NestedObject.Attributes["transition"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatal("transition must be a schema.ListNestedAttribute")
	}
	for _, want := range []string{"days", "date", "storage_class"} {
		if _, ok := transition.NestedObject.Attributes[want]; !ok {
			t.Errorf("missing transition attribute %q", want)
		}
	}

	ncTransition, ok := ruleAttr.NestedObject.Attributes["noncurrent_version_transition"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatal("noncurrent_version_transition must be a schema.ListNestedAttribute")
	}
	for _, want := range []string{"noncurrent_days", "newer_noncurrent_versions", "storage_class"} {
		if _, ok := ncTransition.NestedObject.Attributes[want]; !ok {
			t.Errorf("missing noncurrent_version_transition attribute %q", want)
		}
	}

	ncExpiration, ok := ruleAttr.NestedObject.Attributes["noncurrent_version_expiration"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("noncurrent_version_expiration must be a schema.SingleNestedAttribute")
	}
	for _, want := range []string{"noncurrent_days", "newer_noncurrent_versions"} {
		if _, ok := ncExpiration.Attributes[want]; !ok {
			t.Errorf("missing noncurrent_version_expiration attribute %q", want)
		}
	}

	abort, ok := ruleAttr.NestedObject.Attributes["abort_incomplete_multipart_upload"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("abort_incomplete_multipart_upload must be a schema.SingleNestedAttribute")
	}
	if _, ok := abort.Attributes["days_after_initiation"]; !ok {
		t.Error("missing abort_incomplete_multipart_upload.days_after_initiation attribute")
	}
}

func TestLifecycleRuleExpandFlattenRoundTrip(t *testing.T) {
	plan := []ruleModel{{
		Id:     types.StringValue("all-actions"),
		Status: types.StringValue("Enabled"),
		Filter: &filterModel{
			Prefix:                types.StringValue("logs/"),
			ObjectSizeGreaterThan: types.Int64Value(100),
			ObjectSizeLessThan:    types.Int64Value(200),
			Tag:                   &tagModel{Key: types.StringValue("team"), Value: types.StringValue("core")},
			And: &filterAndModel{
				Prefix:                types.StringValue("and/"),
				Tags:                  []tagModel{{Key: types.StringValue("k2"), Value: types.StringValue("v2")}},
				ObjectSizeGreaterThan: types.Int64Value(10),
				ObjectSizeLessThan:    types.Int64Value(20),
			},
		},
		Expiration: &expirationModel{
			Days:                      types.Int64Value(30),
			Date:                      types.StringValue("2026-12-31T00:00:00Z"),
			ExpiredObjectDeleteMarker: types.BoolValue(true),
		},
		Transition: []transitionModel{
			{Days: types.Int64Value(60), StorageClass: types.StringValue("WARM")},
			{Date: types.StringValue("2026-06-30T00:00:00Z"), StorageClass: types.StringValue("COLD")},
		},
		NoncurrentVersionExpiration: &noncurrentVersionExpirationModel{
			NoncurrentDays:          types.Int64Value(90),
			NewerNoncurrentVersions: types.Int64Value(3),
		},
		NoncurrentVersionTransition: []noncurrentVersionTransitionModel{
			{NoncurrentDays: types.Int64Value(45), NewerNoncurrentVersions: types.Int64Value(2), StorageClass: types.StringValue("COLD")},
		},
		AbortIncompleteMultipartUpload: &abortIncompleteMultipartUploadModel{
			DaysAfterInitiation: types.Int64Value(7),
		},
	}}

	api, err := expandLifecycleRules(plan)
	if err != nil {
		t.Fatalf("expandLifecycleRules: %v", err)
	}
	if len(api) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(api))
	}
	if api[0].Filter == nil || api[0].Filter.And == nil || len(api[0].Filter.And.Tags) != 1 {
		t.Fatal("filter.and not expanded")
	}
	if len(api[0].Transitions) != 2 {
		t.Fatalf("expected 2 transitions, got %d", len(api[0].Transitions))
	}

	got := flattenLifecycleRules(api)
	if len(got) != 1 {
		t.Fatalf("expected 1 flattened rule, got %d", len(got))
	}
	r := got[0]

	if r.Id.ValueString() != "all-actions" || r.Status.ValueString() != "Enabled" {
		t.Errorf("rule identity not round-tripped: id=%q status=%q", r.Id.ValueString(), r.Status.ValueString())
	}
	if r.Filter == nil || r.Filter.Prefix.ValueString() != "logs/" {
		t.Fatal("filter prefix not round-tripped")
	}
	if r.Filter.ObjectSizeGreaterThan.ValueInt64() != 100 || r.Filter.ObjectSizeLessThan.ValueInt64() != 200 {
		t.Error("filter object size bounds not round-tripped")
	}
	if r.Filter.Tag == nil || r.Filter.Tag.Key.ValueString() != "team" || r.Filter.Tag.Value.ValueString() != "core" {
		t.Error("filter tag not round-tripped")
	}
	if r.Filter.And == nil || r.Filter.And.Prefix.ValueString() != "and/" || len(r.Filter.And.Tags) != 1 {
		t.Fatal("filter.and not round-tripped")
	}
	if r.Expiration == nil || r.Expiration.Days.ValueInt64() != 30 {
		t.Fatal("expiration days not round-tripped")
	}
	if r.Expiration.Date.ValueString() != "2026-12-31T00:00:00Z" {
		t.Errorf("expiration date not round-tripped: %q", r.Expiration.Date.ValueString())
	}
	if !r.Expiration.ExpiredObjectDeleteMarker.ValueBool() {
		t.Error("expired_object_delete_marker not round-tripped")
	}
	if len(r.Transition) != 2 {
		t.Fatalf("expected 2 transitions after flatten, got %d", len(r.Transition))
	}
	if r.Transition[0].Days.ValueInt64() != 60 || r.Transition[0].StorageClass.ValueString() != "WARM" {
		t.Error("day-based transition not round-tripped")
	}
	if r.Transition[1].Date.ValueString() != "2026-06-30T00:00:00Z" || r.Transition[1].StorageClass.ValueString() != "COLD" {
		t.Error("date-based transition not round-tripped")
	}
	if r.NoncurrentVersionExpiration == nil || r.NoncurrentVersionExpiration.NoncurrentDays.ValueInt64() != 90 {
		t.Fatal("noncurrent_version_expiration not round-tripped")
	}
	if r.NoncurrentVersionExpiration.NewerNoncurrentVersions.ValueInt64() != 3 {
		t.Error("noncurrent_version_expiration.newer_noncurrent_versions not round-tripped")
	}
	if len(r.NoncurrentVersionTransition) != 1 || r.NoncurrentVersionTransition[0].NewerNoncurrentVersions.ValueInt64() != 2 {
		t.Fatal("noncurrent_version_transition not round-tripped")
	}
	if r.AbortIncompleteMultipartUpload == nil || r.AbortIncompleteMultipartUpload.DaysAfterInitiation.ValueInt64() != 7 {
		t.Fatal("abort_incomplete_multipart_upload not round-tripped")
	}
}

func TestLifecycleHelpers(t *testing.T) {
	if int32Pointer(types.Int64Null()) != nil || int32Pointer(types.Int64Unknown()) != nil {
		t.Error("int32Pointer must return nil for null/unknown values")
	}
	if v := int32Pointer(types.Int64Value(42)); v == nil || *v != 42 {
		t.Error("int32Pointer must convert known values")
	}
	if int64Pointer(types.Int64Null()) != nil {
		t.Error("int64Pointer must return nil for null values")
	}
	if v := int64Pointer(types.Int64Value(42)); v == nil || *v != 42 {
		t.Error("int64Pointer must convert known values")
	}
	if v, err := datePointer(types.StringNull()); err != nil || v != nil {
		t.Error("datePointer must return nil for null values")
	}
	if _, err := datePointer(types.StringValue("not-a-date")); err == nil {
		t.Error("datePointer must reject invalid dates")
	}
	if v, err := datePointer(types.StringValue("2026-12-31T00:00:00Z")); err != nil || v == nil || !v.Equal(mustDate(t, "2026-12-31T00:00:00Z")) {
		t.Error("datePointer must parse RFC3339 dates")
	}
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	return parsed.UTC()
}

func TestLifecycleNotFoundDetection(t *testing.T) {
	for _, msg := range []string{"NoSuchLifecycleConfiguration", "NoSuchBucket"} {
		if !isLifecycleNotFound(errors.New(msg)) {
			t.Errorf("expected %q to be treated as not found", msg)
		}
	}
	for _, msg := range []string{"access denied", "cannot reach host api-404.internal"} {
		if isLifecycleNotFound(errors.New(msg)) {
			t.Errorf("expected %q not to be treated as not found", msg)
		}
	}
}

func TestBucketLifecycleConfigurationEnumValidators(t *testing.T) {
	r := NewBucketLifecycleConfigurationResource()
	resp := frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), frameworkresource.SchemaRequest{}, &resp)

	ruleAttr, ok := resp.Schema.Attributes["rule"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatal("rule must be a schema.ListNestedAttribute")
	}
	status, ok := ruleAttr.NestedObject.Attributes["status"].(schema.StringAttribute)
	if !ok {
		t.Fatal("rule.status must be a schema.StringAttribute")
	}
	assertStringEnumValidator(t, status.Validators, "Enabled", "Bogus")

	minimum, ok := resp.Schema.Attributes["transition_default_minimum_object_size"].(schema.StringAttribute)
	if !ok {
		t.Fatal("transition_default_minimum_object_size must be a schema.StringAttribute")
	}
	assertStringEnumValidator(t, minimum.Validators, "all_storage_classes_128K", "Bogus")
}

func assertStringEnumValidator(t *testing.T, validators []validator.String, valid, invalid string) {
	t.Helper()
	if len(validators) == 0 {
		t.Fatal("expected at least one string validator")
	}
	rejected := func(value string) bool {
		var vresp validator.StringResponse
		for _, v := range validators {
			v.ValidateString(context.Background(), validator.StringRequest{
				Path:        path.Root("test"),
				ConfigValue: types.StringValue(value),
			}, &vresp)
		}
		return vresp.Diagnostics.HasError()
	}
	if rejected(valid) {
		t.Errorf("validator rejected valid value %q", valid)
	}
	if !rejected(invalid) {
		t.Errorf("validator accepted invalid value %q", invalid)
	}
}

func TestValidateLifecycleFilters(t *testing.T) {
	twoPredicates := &filterAndModel{
		Prefix:                types.StringValue("archive/"),
		ObjectSizeGreaterThan: types.Int64Value(1024),
	}
	twoTags := &filterAndModel{
		Tags: []tagModel{
			{Key: types.StringValue("a"), Value: types.StringValue("1")},
			{Key: types.StringValue("b"), Value: types.StringValue("2")},
		},
	}

	tests := []struct {
		name   string
		filter *filterModel
		want   int
	}{
		{"nil filter", nil, 0},
		{"prefix only", &filterModel{Prefix: types.StringValue("logs/")}, 0},
		{"tag only", &filterModel{Tag: &tagModel{Key: types.StringValue("k"), Value: types.StringValue("v")}}, 0},
		{"and with two predicates", &filterModel{And: twoPredicates}, 0},
		{"and with two tags", &filterModel{And: twoTags}, 0},
		{"and with prefix and tag sibling", &filterModel{
			Tag: &tagModel{Key: types.StringValue("a"), Value: types.StringValue("1")},
			And: twoPredicates,
		}, 1},
		{"and with prefix sibling", &filterModel{Prefix: types.StringValue("logs/"), And: twoPredicates}, 1},
		{"and with size sibling", &filterModel{ObjectSizeLessThan: types.Int64Value(100), And: twoPredicates}, 1},
		{"and with one predicate", &filterModel{And: &filterAndModel{Prefix: types.StringValue("archive/")}}, 1},
		{"and empty", &filterModel{And: &filterAndModel{}}, 1},
		{"and with one predicate and sibling", &filterModel{
			Tag: &tagModel{Key: types.StringValue("a"), Value: types.StringValue("1")},
			And: &filterAndModel{Prefix: types.StringValue("archive/")},
		}, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules := []ruleModel{{Id: types.StringValue("r"), Filter: tc.filter}}
			got := validateLifecycleFilters(rules)
			if len(got) != tc.want {
				t.Fatalf("got %d violations, want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

func TestBucketLifecycleConfigurationConfigValidators(t *testing.T) {
	r := NewBucketLifecycleConfigurationResource()
	withValidators, ok := r.(frameworkresource.ResourceWithConfigValidators)
	if !ok {
		t.Fatal("lifecycle resource must implement ResourceWithConfigValidators")
	}
	validators := withValidators.ConfigValidators(context.Background())
	if len(validators) == 0 {
		t.Fatal("expected at least one config validator")
	}
	for _, v := range validators {
		if v.Description(context.Background()) == "" {
			t.Error("config validator must carry a description")
		}
	}
}

// TestLifecycleFilterConfigValidatorMarkdownDescription pins the validator's
// markdown text to its plain description: lifecycleFilterConfigValidator
// implements MarkdownDescription by returning Description verbatim.
func TestLifecycleFilterConfigValidatorMarkdownDescription(t *testing.T) {
	v := lifecycleFilterConfigValidator{}
	want := v.Description(context.Background())
	if want == "" {
		t.Fatal("Description must not be empty")
	}
	if got := v.MarkdownDescription(context.Background()); got != want {
		t.Errorf("MarkdownDescription = %q, want %q", got, want)
	}
}

func TestAccBucketLifecycleConfigurationResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-lc-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_lifecycle_configuration.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccLifecycleConfig(name, "Enabled", "logs/", 30),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "rule1"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.status", "Enabled"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.filter.prefix", "logs/"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.expiration.days", "30"),
				),
			},
			{
				Config: testAccLifecycleConfig(name, "Disabled", "archive/", 90),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.status", "Disabled"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.filter.prefix", "archive/"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.expiration.days", "90"),
				),
			},
		},
	})
}

func testAccLifecycleConfig(bucket, status, prefix string, days int) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_lifecycle_configuration" "test" {
  bucket = rustfs_bucket.test.name

  rule = [
    {
      id     = "rule1"
      status = "%s"

      filter = {
        prefix = "%s"
      }

      expiration = {
        days = %d
      }
    }
  ]
}
`, bucket, status, prefix, days)
}

func TestAccBucketLifecycleConfigurationResource_full(t *testing.T) {
	name := fmt.Sprintf("tf-test-lc-full-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_lifecycle_configuration.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccLifecycleFullConfig(name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "expire-days"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.filter.prefix", "logs/"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.expiration.days", "30"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.noncurrent_version_expiration.noncurrent_days", "90"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.abort_incomplete_multipart_upload.days_after_initiation", "7"),

					resource.TestCheckResourceAttr(resourceName, "rule.1.id", "expire-marker"),
					resource.TestCheckResourceAttr(resourceName, "rule.1.filter.prefix", "tombstones/"),
					resource.TestCheckResourceAttr(resourceName, "rule.1.expiration.expired_object_delete_marker", "true"),

					resource.TestCheckResourceAttr(resourceName, "rule.2.id", "tagged-and-sized"),
					resource.TestCheckResourceAttr(resourceName, "rule.2.filter.and.prefix", "archive/"),
					resource.TestCheckResourceAttr(resourceName, "rule.2.filter.and.tags.0.key", "tier"),
					resource.TestCheckResourceAttr(resourceName, "rule.2.filter.and.tags.0.value", "cold"),
				),
			},
		},
	})
}

// testAccLifecycleFullConfig exercises the lifecycle fields RustFS accepts on a
// bare server. Transitions (rule.transition / rule.noncurrent_version_transition)
// are intentionally omitted here: RustFS rejects a storage class that does not
// correspond to a pre-configured ILM tier ("invalid tier"), which this harness
// does not provision. Transition mapping is covered live-independent by
// TestLifecycleExpandFlatten.
func testAccLifecycleFullConfig(bucket string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_lifecycle_configuration" "test" {
  bucket = rustfs_bucket.test.name

  rule = [
    {
      id     = "expire-days"
      status = "Enabled"

      filter = {
        prefix = "logs/"
      }

      expiration = {
        days = 30
      }

      noncurrent_version_expiration = {
        noncurrent_days = 90
      }

      abort_incomplete_multipart_upload = {
        days_after_initiation = 7
      }
    },
    {
      id     = "expire-marker"
      status = "Enabled"

      filter = {
        prefix = "tombstones/"
      }

      expiration = {
        expired_object_delete_marker = true
      }
    },
    {
      id     = "tagged-and-sized"
      status = "Enabled"

      filter = {
        and = {
          prefix = "archive/"

          tags = [
            {
              key   = "tier"
              value = "cold"
            }
          ]

          object_size_greater_than = 1024
          object_size_less_than    = 1048576
        }
      }

      expiration = {
        date = "2027-01-01T00:00:00Z"
      }
    }
  ]
}
`, bucket)
}

// TestAccBucketLifecycleConfigurationResource_updateImportReplaceDrift covers
// the lifecycle paths the create/destroy oriented tests do not: an in-place
// rule update with the value read back from the server, its revert, import with
// verification, an explicit empty second plan, the RequiresReplace plan for
// bucket (the only attribute carrying RequiresReplace) applied for real, and
// out-of-band drift repaired by the next apply.
func TestAccBucketLifecycleConfigurationResource_updateImportReplaceDrift(t *testing.T) {
	name := fmt.Sprintf("tf-test-lc-upd-%d", acctest.RandInt())
	altName := fmt.Sprintf("tf-test-lc-alt-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_lifecycle_configuration.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			// Create.
			{
				Config: testAccLifecycleUpdateConfig(name, altName, false, "logs/", 30),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", name),
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "rule1"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.expiration.days", "30"),
					testAccCheckBucketLifecycleRuleOnServer(resourceName, "rule1", "logs/", 30),
				),
			},
			// In-place update: prefix and expiration days both change, and the
			// server must report the new values.
			{
				Config: testAccLifecycleUpdateConfig(name, altName, false, "archive/", 90),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", name),
					resource.TestCheckResourceAttr(resourceName, "rule.0.filter.prefix", "archive/"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.expiration.days", "90"),
					testAccCheckBucketLifecycleRuleOnServer(resourceName, "rule1", "archive/", 90),
				),
			},
			// Revert to the original values.
			{
				Config: testAccLifecycleUpdateConfig(name, altName, false, "logs/", 30),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.filter.prefix", "logs/"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.expiration.days", "30"),
					testAccCheckBucketLifecycleRuleOnServer(resourceName, "rule1", "logs/", 30),
				),
			},
			// ImportStatePassthroughID on the bucket name, verified against the
			// state written by the steps above.
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
			// Computed consistency: the recorded state produces an empty plan.
			{
				Config:             testAccLifecycleUpdateConfig(name, altName, false, "logs/", 30),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// bucket carries RequiresReplace, so pointing the configuration at
			// another bucket must be planned as a replacement, not an update.
			{
				Config:             testAccLifecycleUpdateConfig(name, altName, true, "logs/", 30),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			// Apply the replacement so the replacement path really runs.
			{
				Config: testAccLifecycleUpdateConfig(name, altName, true, "logs/", 30),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", altName),
					resource.TestCheckResourceAttr(resourceName, "rule.0.expiration.days", "30"),
					testAccCheckBucketLifecycleRuleOnServer(resourceName, "rule1", "logs/", 30),
				),
			},
			// Out-of-band drift: rewrite the rules behind Terraform's back. The
			// mutation runs inside Check, before the post-apply plans, so the
			// non-refresh plan is empty while the refreshed plan must not be.
			{
				Config:             testAccLifecycleUpdateConfig(name, altName, true, "logs/", 30),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketLifecycleRuleOnServer(resourceName, "rule1", "logs/", 30),
					testAccMutateBucketLifecycleOutOfBand(t, resourceName, "drift/", 4321),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			// The next apply converges back to the configured rules.
			{
				Config: testAccLifecycleUpdateConfig(name, altName, true, "logs/", 30),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "rule1"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.filter.prefix", "logs/"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.expiration.days", "30"),
					testAccCheckBucketLifecycleRuleOnServer(resourceName, "rule1", "logs/", 30),
				),
			},
		},
	})
}

// testAccLifecycleUpdateConfig declares both buckets so the lifecycle resource
// can be moved between them; useAlt selects which bucket carries the rules.
func testAccLifecycleUpdateConfig(bucket, altBucket string, useAlt bool, prefix string, days int) string {
	target := "rustfs_bucket.test.name"
	if useAlt {
		target = "rustfs_bucket.alt.name"
	}
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket" "alt" {
  name = "%s"
}

resource "rustfs_bucket_lifecycle_configuration" "test" {
  bucket = %s

  rule = [
    {
      id     = "rule1"
      status = "Enabled"

      filter = {
        prefix = "%s"
      }

      expiration = {
        days = %d
      }
    }
  ]
}
`, bucket, altBucket, target, prefix, days)
}

// testAccCheckBucketLifecycleRuleOnServer reads the lifecycle configuration
// straight from the server and asserts the single expected rule is present with
// the given prefix and expiration days.
func testAccCheckBucketLifecycleRuleOnServer(resourceName, wantID, wantPrefix string, wantDays int32) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %s not found in state", resourceName)
		}
		bucket := rs.Primary.Attributes["bucket"]
		if bucket == "" {
			return fmt.Errorf("resource %s has no bucket in state", resourceName)
		}

		client, err := testAccS3Client()
		if err != nil {
			return err
		}

		out, err := client.GetBucketLifecycleConfiguration(context.Background(), &s3.GetBucketLifecycleConfigurationInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("reading lifecycle configuration of bucket %s: %w", bucket, err)
		}
		if len(out.Rules) != 1 {
			return fmt.Errorf("bucket %s has %d lifecycle rules on the server, want 1", bucket, len(out.Rules))
		}

		rule := out.Rules[0]
		if got := aws.ToString(rule.ID); got != wantID {
			return fmt.Errorf("bucket %s rule ID = %q on the server, want %q", bucket, got, wantID)
		}
		if got := aws.ToString(rule.Filter.Prefix); got != wantPrefix {
			return fmt.Errorf("bucket %s rule prefix = %q on the server, want %q", bucket, got, wantPrefix)
		}
		if rule.Expiration == nil {
			return fmt.Errorf("bucket %s rule %q has no expiration on the server", bucket, wantID)
		}
		if got := aws.ToInt32(rule.Expiration.Days); got != wantDays {
			return fmt.Errorf("bucket %s rule expiration days = %d on the server, want %d", bucket, got, wantDays)
		}
		return nil
	}
}

// testAccMutateBucketLifecycleOutOfBand replaces the bucket's lifecycle rules
// through the S3 client, leaving the Terraform state describing the old rules.
func testAccMutateBucketLifecycleOutOfBand(t *testing.T, resourceName, prefix string, days int32) resource.TestCheckFunc {
	t.Helper()
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %s not found in state", resourceName)
		}
		bucket := rs.Primary.Attributes["bucket"]
		if bucket == "" {
			return fmt.Errorf("resource %s has no bucket in state", resourceName)
		}

		client, err := testAccS3Client()
		if err != nil {
			return err
		}

		_, err = client.PutBucketLifecycleConfiguration(context.Background(), &s3.PutBucketLifecycleConfigurationInput{
			Bucket: aws.String(bucket),
			LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{
				Rules: []s3types.LifecycleRule{{
					ID:         aws.String("drifted"),
					Status:     s3types.ExpirationStatusEnabled,
					Filter:     &s3types.LifecycleRuleFilter{Prefix: aws.String(prefix)},
					Expiration: &s3types.LifecycleExpiration{Days: aws.Int32(days)},
				}},
			},
		})
		if err != nil {
			return fmt.Errorf("mutating lifecycle configuration of bucket %s out of band: %w", bucket, err)
		}
		return nil
	}
}

// Migrated from main's read/delete/create error-path work.
func TestBucketLifecycleConfigurationResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	newModel := func() *bucketLifecycleConfigurationModel {
		ruleList, diags := ruleListFromModels(context.Background(), []ruleModel{{
			Id:         types.StringValue("rule-1"),
			Status:     types.StringValue("Enabled"),
			Filter:     &filterModel{Prefix: types.StringValue("logs/")},
			Expiration: &expirationModel{Days: types.Int64Value(30)},
		}})
		if diags.HasError() {
			t.Fatalf("building rule list: %s", diagnosticsError(diags))
		}
		return &bucketLifecycleConfigurationModel{
			Bucket: types.StringValue("tf-test"),
			Id:     types.StringValue("tf-test"),
			Rule:   ruleList,
		}
	}

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
		wantServer  string
	}{
		{
			// S3 reports a missing lifecycle sub-resource as
			// NoSuchLifecycleConfiguration.
			name:        "absent lifecycle configuration is removed from state",
			routes:      []requestRoute{{method: http.MethodGet, status: http.StatusNotFound, body: s3ErrorXML("NoSuchLifecycleConfiguration", "The lifecycle configuration does not exist")}},
			wantRemoved: true,
		},
		{
			// Live RustFS answers 404 NoSuchBucket when the bucket itself is gone.
			name:        "missing bucket is removed from state",
			routes:      []requestRoute{{method: http.MethodGet, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{method: http.MethodGet, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "access denied")}},
			wantSummary: "Error reading bucket lifecycle configuration",
			wantDetail:  "Could not read lifecycle configuration: ",
			wantServer:  "access denied",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketLifecycleConfigurationResource(), client)

			resp := h.read(newModel())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, tc.wantServer)
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name       string
		routes     []requestRoute
		wantTitle  string
		wantDetail string
		wantServer string
	}{
		{
			name:   "already deleted lifecycle configuration is not an error",
			routes: []requestRoute{{method: http.MethodDelete, status: http.StatusNotFound, body: s3ErrorXML("NoSuchLifecycleConfiguration", "The lifecycle configuration does not exist")}},
		},
		{
			name:   "missing bucket is not an error",
			routes: []requestRoute{{method: http.MethodDelete, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
		},
		{
			name:       "real server error is reported",
			routes:     []requestRoute{{method: http.MethodDelete, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "access denied")}},
			wantTitle:  "Error deleting bucket lifecycle configuration",
			wantDetail: "Could not delete lifecycle configuration: ",
			wantServer: "access denied",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketLifecycleConfigurationResource(), client)

			resp := h.delete(newModel())

			if tc.wantTitle == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantTitle, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, tc.wantServer)
		})
	}

	createCases := []struct {
		name       string
		routes     []requestRoute
		plan       *bucketLifecycleConfigurationModel
		wantTitle  string
		wantDetail string
		wantServer string
	}{
		{
			// The server rejects this payload as malformed.
			name: "rejected payload reports the server error and writes no state",
			routes: []requestRoute{{
				method: http.MethodPut,
				status: http.StatusBadRequest,
				body:   s3ErrorXML("MalformedXML", "The XML you provided was not well-formed or did not validate"),
			}},
			plan:       newModel(),
			wantTitle:  "Error creating bucket lifecycle configuration",
			wantDetail: "Could not create lifecycle configuration: ",
			wantServer: "did not validate",
		},
		{
			// Missing prerequisite: the bucket is gone.
			name: "missing bucket reports the server error and writes no state",
			routes: []requestRoute{{
				method: http.MethodPut,
				status: http.StatusNotFound,
				body:   s3ErrorXML("NoSuchBucket", "The specified bucket does not exist"),
			}},
			plan:       newModel(),
			wantTitle:  "Error creating bucket lifecycle configuration",
			wantDetail: "Could not create lifecycle configuration: ",
			wantServer: "NoSuchBucket",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketLifecycleConfigurationResource(), client)

			resp := h.create(tc.plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantTitle, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, tc.wantServer)
			h.assertNoPartialState(resp, tc.plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketLifecycleConfigurationResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

// TestBucketLifecycleConfigurationRuleUnknownDecodes is the regression test for
// the "Received unknown value ... Target Type: []provider.ruleModel" failure.
// Before the fix, Rule was a native []ruleModel that could not hold an unknown
// value, so a config whose rules came from count/for_each or any computed value
// failed to decode. Now Rule is a types.List: an unknown or null list must
// decode to no rules without diagnostics, and a known list must round-trip.
func TestBucketLifecycleConfigurationRuleUnknownDecodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	elemType := ruleElementType(ctx)

	t.Run("unknown rule list decodes to no rules", func(t *testing.T) {
		rules, diags := ruleModelsFromList(ctx, types.ListUnknown(elemType))
		if diags.HasError() {
			t.Fatalf("unknown list must not error, got: %s", diagnosticsError(diags))
		}
		if rules != nil {
			t.Fatalf("unknown list must decode to nil rules, got %d", len(rules))
		}
	})

	t.Run("null rule list decodes to no rules", func(t *testing.T) {
		rules, diags := ruleModelsFromList(ctx, types.ListNull(elemType))
		if diags.HasError() {
			t.Fatalf("null list must not error, got: %s", diagnosticsError(diags))
		}
		if rules != nil {
			t.Fatalf("null list must decode to nil rules, got %d", len(rules))
		}
	})

	t.Run("known list with an unknown element decodes to a placeholder", func(t *testing.T) {
		known, diags := ruleListFromModels(ctx, []ruleModel{{
			Id:     types.StringValue("known"),
			Status: types.StringValue("Enabled"),
		}})
		if diags.HasError() {
			t.Fatalf("building known rule: %s", diagnosticsError(diags))
		}
		list, diags := types.ListValue(elemType, []attr.Value{unknownRuleElement(t, ctx), known.Elements()[0]})
		if diags.HasError() {
			t.Fatalf("building mixed rule list: %s", diagnosticsError(diags))
		}

		rules, diags := ruleModelsFromList(ctx, list)
		if diags.HasError() {
			t.Fatalf("unknown element must not error, got: %s", diagnosticsError(diags))
		}
		if len(rules) != 2 {
			t.Fatalf("expected 2 rules (unknown element replaced by a placeholder), got %d", len(rules))
		}
		if !rules[0].Id.IsNull() || rules[0].Filter != nil {
			t.Errorf("unknown element must decode to a zero-value placeholder, got %+v", rules[0])
		}
		if rules[1].Id.ValueString() != "known" {
			t.Errorf("known element shifted or lost: %+v", rules[1])
		}
	})

	t.Run("absent nested options stay null", func(t *testing.T) {
		want := []ruleModel{{
			Id:         types.StringValue("only-expiration"),
			Status:     types.StringValue("Enabled"),
			Expiration: &expirationModel{Days: types.Int64Value(1)},
		}}

		list, diags := ruleListFromModels(ctx, want)
		if diags.HasError() {
			t.Fatalf("building list: %s", diagnosticsError(diags))
		}
		got, diags := ruleModelsFromList(ctx, list)
		if diags.HasError() {
			t.Fatalf("decoding list: %s", diagnosticsError(diags))
		}
		if got[0].Filter != nil {
			t.Errorf("absent filter decoded to %+v, want nil", got[0].Filter)
		}
		if got[0].Transition != nil {
			t.Errorf("absent transition decoded to %+v, want nil", got[0].Transition)
		}
		if got[0].NoncurrentVersionExpiration != nil {
			t.Errorf("absent noncurrent_version_expiration decoded to %+v, want nil", got[0].NoncurrentVersionExpiration)
		}
		if got[0].NoncurrentVersionTransition != nil {
			t.Errorf("absent noncurrent_version_transition decoded to %+v, want nil", got[0].NoncurrentVersionTransition)
		}
		if got[0].AbortIncompleteMultipartUpload != nil {
			t.Errorf("absent abort_incomplete_multipart_upload decoded to %+v, want nil", got[0].AbortIncompleteMultipartUpload)
		}
	})

	t.Run("known rule list round-trips", func(t *testing.T) {
		want := []ruleModel{{
			Id:         types.StringValue("archive-to-cold"),
			Status:     types.StringValue("Enabled"),
			Filter:     &filterModel{Prefix: types.StringValue("movies/"), ObjectSizeGreaterThan: types.Int64Value(1000000)},
			Transition: []transitionModel{{Days: types.Int64Value(0), StorageClass: types.StringValue("COLD")}},
		}}

		list, diags := ruleListFromModels(ctx, want)
		if diags.HasError() {
			t.Fatalf("building list: %s", diagnosticsError(diags))
		}
		if list.IsNull() || list.IsUnknown() {
			t.Fatal("known rules must build a known, non-null list")
		}

		got, diags := ruleModelsFromList(ctx, list)
		if diags.HasError() {
			t.Fatalf("decoding list: %s", diagnosticsError(diags))
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(got))
		}
		if got[0].Id.ValueString() != "archive-to-cold" || got[0].Status.ValueString() != "Enabled" {
			t.Errorf("identity not round-tripped: %+v", got[0])
		}
		if got[0].Filter == nil || got[0].Filter.Prefix.ValueString() != "movies/" {
			t.Errorf("filter not round-tripped: %+v", got[0].Filter)
		}
		if len(got[0].Transition) != 1 || got[0].Transition[0].StorageClass.ValueString() != "COLD" {
			t.Errorf("transition not round-tripped: %+v", got[0].Transition)
		}
	})
}

// unknownRuleElement is a rule object whose value is unknown, as happens when
// the rule comes from a value that is not known until apply. Terraform may
// resolve the list itself to a known list while one of its elements stays
// unknown, so the decoder has to tolerate the element, not only the list.
func unknownRuleElement(t *testing.T, ctx context.Context) types.Object {
	t.Helper()

	elemType, ok := ruleElementType(ctx).(types.ObjectType)
	if !ok {
		t.Fatalf("rule element type = %T, want types.ObjectType", ruleElementType(ctx))
	}
	return types.ObjectUnknown(elemType.AttrTypes)
}

// TestBucketLifecycleConfigurationValidateResourceToleratesUnknownRules is the
// regression test for the config validator rejecting a plan whose rule list is
// not fully known yet. The whole list can be unknown (count/for_each) or the
// list can be known with one unknown element (`rule = [some_unknown_object]`);
// both must validate without a "Received unknown value" error, and a real
// violation after an unknown element must still point at its original index.
func TestBucketLifecycleConfigurationValidateResourceToleratesUnknownRules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	elemType := ruleElementType(ctx)

	knownList := func(t *testing.T, rules []ruleModel) types.List {
		t.Helper()
		list, diags := ruleListFromModels(ctx, rules)
		if diags.HasError() {
			t.Fatalf("building rule list: %s", diagnosticsError(diags))
		}
		return list
	}
	validate := func(list types.List) *frameworkresource.ValidateConfigResponse {
		h := newCRUDHarness(t, NewBucketLifecycleConfigurationResource(), cannedClient(t, http.StatusOK, ""))
		resp := &frameworkresource.ValidateConfigResponse{}
		lifecycleFilterConfigValidator{}.ValidateResource(ctx, frameworkresource.ValidateConfigRequest{
			Config: h.config(bucketLifecycleConfigurationModel{
				Bucket: types.StringValue("tf-test"),
				Rule:   list,
			}),
		}, resp)
		return resp
	}

	t.Run("unknown rule list", func(t *testing.T) {
		requireNoDiagnostics(t, validate(types.ListUnknown(elemType)).Diagnostics)
	})

	t.Run("known list with an unknown rule element", func(t *testing.T) {
		valid := knownList(t, []ruleModel{{Id: types.StringValue("ok"), Status: types.StringValue("Enabled")}})
		list, diags := types.ListValue(elemType, []attr.Value{unknownRuleElement(t, ctx), valid.Elements()[0]})
		if diags.HasError() {
			t.Fatalf("building mixed rule list: %s", diagnosticsError(diags))
		}
		requireNoDiagnostics(t, validate(list).Diagnostics)
	})

	t.Run("violation after an unknown element keeps its index", func(t *testing.T) {
		invalid := knownList(t, []ruleModel{{
			Id:     types.StringValue("bad"),
			Status: types.StringValue("Enabled"),
			Filter: &filterModel{
				Prefix: types.StringValue("logs/"),
				And: &filterAndModel{Tags: []tagModel{
					{Key: types.StringValue("a"), Value: types.StringValue("1")},
					{Key: types.StringValue("b"), Value: types.StringValue("2")},
				}},
			},
		}})
		list, diags := types.ListValue(elemType, []attr.Value{unknownRuleElement(t, ctx), invalid.Elements()[0]})
		if diags.HasError() {
			t.Fatalf("building mixed rule list: %s", diagnosticsError(diags))
		}

		resp := validate(list)
		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid lifecycle rule filter", "cannot be combined")
		withPath, ok := resp.Diagnostics[0].(diag.DiagnosticWithPath)
		if !ok {
			t.Fatalf("diagnostic %T carries no path", resp.Diagnostics[0])
		}
		want := path.Root("rule").AtListIndex(1).AtName("filter")
		if !withPath.Path().Equal(want) {
			t.Errorf("diagnostic path = %s, want %s", withPath.Path(), want)
		}
	})
}

// TestBucketLifecycleConfigurationCreateRejectsUnknownRuleList proves the write
// path fails loudly instead of sending a lifecycle configuration with the rules
// it could resolve. An unknown list reaching apply is a provider bug, but
// silently writing an empty configuration would be the worst way to report it.
func TestBucketLifecycleConfigurationCreateRejectsUnknownRuleList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	elemType := ruleElementType(ctx)

	known, diags := ruleListFromModels(ctx, []ruleModel{{Id: types.StringValue("kept"), Status: types.StringValue("Enabled")}})
	if diags.HasError() {
		t.Fatalf("building known rule: %s", diagnosticsError(diags))
	}
	mixed, diags := types.ListValue(elemType, []attr.Value{unknownRuleElement(t, ctx), known.Elements()[0]})
	if diags.HasError() {
		t.Fatalf("building mixed rule list: %s", diagnosticsError(diags))
	}

	cases := []struct {
		name string
		list types.List
	}{
		{"whole rule list unknown", types.ListUnknown(elemType)},
		{"one rule element unknown", mixed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, server := routedClient(t)
			h := newCRUDHarness(t, NewBucketLifecycleConfigurationResource(), client)

			plan := &bucketLifecycleConfigurationModel{
				Bucket: types.StringValue("tf-test"),
				Id:     types.StringValue("tf-test"),
				Rule:   tc.list,
			}
			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics,
				"Error creating bucket lifecycle configuration",
				"not fully known before apply")
			server.assertNoRequest(t, http.MethodPut)
			h.assertNoPartialState(resp, plan)
		})
	}
}
