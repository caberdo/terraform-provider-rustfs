package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
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
