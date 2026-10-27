// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const quotaStatsTestPayload = `{
	"bucket": "my-bucket",
	"quota_limit": 1048576,
	"current_usage": 262144,
	"remaining_quota": 786432,
	"usage_percentage": 25
}`

func quotaStatsConfig(bucket string) *QuotaStatsDataSourceModel {
	return &QuotaStatsDataSourceModel{
		ID:              types.StringNull(),
		Bucket:          types.StringValue(bucket),
		QuotaLimit:      types.Int64Null(),
		CurrentUsage:    types.Int64Null(),
		RemainingQuota:  types.Int64Null(),
		UsagePercentage: types.Float64Null(),
	}
}

func readQuotaStats(t *testing.T, d *QuotaStatsDataSource, sch datasourceschema.Schema, model *QuotaStatsDataSourceModel) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

// assertStateNull asserts the attribute at attrPath is null, so a value the
// server did not report is not silently zeroed.
func assertStateNull(t *testing.T, state tfsdk.State, attrPath path.Path) {
	t.Helper()

	var got types.Int64
	if diags := state.GetAttribute(context.Background(), attrPath, &got); diags.HasError() {
		t.Fatalf("state attribute %s: %v", attrPath, diags)
	}
	if !got.IsNull() {
		t.Fatalf("state attribute %s = %d, want null", attrPath, got.ValueInt64())
	}
}

func TestQuotaStatsDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewQuotaStatsDataSource())

	for _, name := range []string{"id", "quota_limit", "current_usage", "remaining_quota", "usage_percentage"} {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
			t.Errorf("attribute %q must be Computed only", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}

	bucket, ok := sch.Attributes["bucket"].(datasourceschema.StringAttribute)
	if !ok || !bucket.Required || bucket.Optional || bucket.Computed {
		t.Errorf("bucket must be Required only: %+v", sch.Attributes["bucket"])
	}
	if bucket.GetDescription() == "" {
		t.Error("bucket has no description")
	}
	if len(bucket.Validators) == 0 {
		t.Error("bucket must reject an empty name")
	}
}

func TestQuotaStatsDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewQuotaStatsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_quota_stats" {
		t.Errorf("expected rustfs_quota_stats, got %s", resp.TypeName)
	}
}

func TestQuotaStatsDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewQuotaStatsDataSource().(*QuotaStatsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewQuotaStatsDataSource().(*QuotaStatsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewQuotaStatsDataSource().(*QuotaStatsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestQuotaStatsDataSourceRead(t *testing.T) {
	t.Run("surfaces the quota view", func(t *testing.T) {
		var gotPath string
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(quotaStatsTestPayload))
		}))
		d := NewQuotaStatsDataSource().(*QuotaStatsDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readQuotaStats(t, d, sch, quotaStatsConfig("my-bucket"))
		requireNoDiagnostics(t, resp.Diagnostics)

		if gotPath != "/rustfs/admin/v3/quota-stats/my-bucket" {
			t.Errorf("path = %q", gotPath)
		}
		assertStateString(t, resp.State, path.Root("id"), "my-bucket")
		assertStateString(t, resp.State, path.Root("bucket"), "my-bucket")
		assertStateInt64(t, resp.State, path.Root("quota_limit"), 1048576)
		assertStateInt64(t, resp.State, path.Root("current_usage"), 262144)
		assertStateInt64(t, resp.State, path.Root("remaining_quota"), 786432)

		var percentage types.Float64
		if diags := resp.State.GetAttribute(context.Background(), path.Root("usage_percentage"), &percentage); diags.HasError() {
			t.Fatalf("reading usage_percentage: %v", diags)
		}
		if percentage.ValueFloat64() != 25 {
			t.Errorf("usage_percentage = %v, want 25", percentage.ValueFloat64())
		}
	})

	t.Run("keeps an unreported value null", func(t *testing.T) {
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"bucket":"my-bucket","quota_limit":null,"current_usage":null,` +
				`"remaining_quota":null,"usage_percentage":null}`))
		}))
		d := NewQuotaStatsDataSource().(*QuotaStatsDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readQuotaStats(t, d, sch, quotaStatsConfig("my-bucket"))
		requireNoDiagnostics(t, resp.Diagnostics)

		assertStateNull(t, resp.State, path.Root("quota_limit"))
		assertStateNull(t, resp.State, path.Root("current_usage"))
		assertStateNull(t, resp.State, path.Root("remaining_quota"))

		var percentage types.Float64
		if diags := resp.State.GetAttribute(context.Background(), path.Root("usage_percentage"), &percentage); diags.HasError() {
			t.Fatalf("reading usage_percentage: %v", diags)
		}
		if !percentage.IsNull() {
			t.Errorf("usage_percentage = %v, want null", percentage.ValueFloat64())
		}
	})

	t.Run("fails for a missing bucket instead of reporting zeros", func(t *testing.T) {
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchBucket</Code><Message>bucket not found: missing</Message></Error>`))
		}))
		d := NewQuotaStatsDataSource().(*QuotaStatsDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readQuotaStats(t, d, sch, quotaStatsConfig("missing"))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading bucket quota statistics")
		if !strings.Contains(diagnostic.Detail(), "bucket not found: missing") {
			t.Errorf("detail = %q", diagnostic.Detail())
		}
	})
}

// testAccQuotaStatsPreCheck proves the running server serves the quota routes.
// A server that registers the path but answers 404/405/501 is reported as an
// *UnsupportedFeatureError, and only that capability gap skips the test.
func testAccQuotaStatsPreCheck(t *testing.T) {
	testAccPreCheck(t)
	client := testAccRustClient()
	if _, err := client.GetQuotaStats("tf-route-probe"); err != nil && rustfs.IsUnsupportedFeature(err) {
		t.Skipf("the quota-stats route is not implemented by the running RustFS, skipping: %v", err)
	}
}

func testAccQuotaCheckPreCheck(t *testing.T) {
	testAccPreCheck(t)
	client := testAccRustClient()
	if _, err := client.CheckQuota("tf-route-probe", "PUT", 1); err != nil && rustfs.IsUnsupportedFeature(err) {
		t.Skipf("the quota-check route is not implemented by the running RustFS, skipping: %v", err)
	}
}

// waitForQuotaUsage polls the quota view until the server reports non-zero
// usage for the bucket. The write that creates the usage happens in a previous
// step, but the quota subsystem may publish it slightly later, which would make
// a single read flaky.
func waitForQuotaUsage(t *testing.T, bucket string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		client := testAccRustClient()
		deadline := time.Now().Add(30 * time.Second)
		for {
			stats, err := client.GetQuotaStats(bucket)
			if err == nil && stats.CurrentUsage != nil && *stats.CurrentUsage > 0 {
				return nil
			}
			if time.Now().After(deadline) {
				if err != nil {
					return fmt.Errorf("quota usage for %q did not become non-zero: %w", bucket, err)
				}
				return fmt.Errorf("quota usage for %q stayed zero", bucket)
			}
			time.Sleep(time.Second)
		}
	}
}

// testAccWriteQuotaObject writes one object into the bucket through the S3 data
// plane so the quota subsystem has usage to report.
func testAccWriteQuotaObject(bucket string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("quota-stats/object.txt"),
			Body:   strings.NewReader("quota stats acceptance fixture"),
		})
		return err
	}
}

func TestAccQuotaStatsDataSource(t *testing.T) {
	bucketName := fmt.Sprintf("tf-acc-quota-stats-%d", acctest.RandInt())
	dataSourceName := "data.rustfs_quota_stats.test"
	const quota = 1048576

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccQuotaStatsPreCheck(t) },
		CheckDestroy:             testAccCheckQuotaAndBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccQuotaStatsResources(bucketName, quota),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccWriteQuotaObject(bucketName),
					waitForQuotaUsage(t, bucketName),
				),
			},
			{
				Config: testAccQuotaStatsConfig(bucketName, quota, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(dataSourceName, "id", bucketName),
					resource.TestCheckResourceAttr(dataSourceName, "bucket", bucketName),
					resource.TestCheckResourceAttr(dataSourceName, "quota_limit", fmt.Sprint(quota)),
					resource.TestCheckResourceAttrSet(dataSourceName, "current_usage"),
					resource.TestCheckResourceAttrSet(dataSourceName, "remaining_quota"),
					resource.TestCheckResourceAttrSet(dataSourceName, "usage_percentage"),
					testCheckQuotaStatsUsageNonZero(dataSourceName),
				),
			},
			{
				// Destroying the quota resource clears the limit. The data source
				// read in this apply may still run before the destroy, so this step
				// only drops the resource.
				Config: testAccQuotaStatsConfig(bucketName, quota, false),
				Check:  resource.TestCheckResourceAttr(dataSourceName, "id", bucketName),
			},
			{
				// A second apply of the same configuration reads the stats after the
				// quota is gone and must report null instead of zero.
				Config: testAccQuotaStatsConfig(bucketName, quota, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					testCheckAttributeNull(dataSourceName, "quota_limit"),
				),
			},
		},
	})
}

func testAccQuotaStatsResources(bucket string, quota int) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name          = %q
  force_destroy = true
}

resource "rustfs_quota" "test" {
  bucket     = rustfs_bucket.test.name
  quota      = %d
  depends_on = [rustfs_bucket.test]
}
`, bucket, quota)
}

func testAccQuotaStatsConfig(bucket string, quota int, withQuota bool) string {
	config := testAccQuotaStatsResources(bucket, quota)
	if !withQuota {
		config = testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name          = %q
  force_destroy = true
}
`, bucket)
	}
	return config + `
data "rustfs_quota_stats" "test" {
  bucket     = rustfs_bucket.test.name
  depends_on = [rustfs_bucket.test]
}
`
}

func TestAccQuotaCheckDataSource(t *testing.T) {
	bucketName := fmt.Sprintf("tf-acc-quota-check-%d", acctest.RandInt())
	dataSourceName := "data.rustfs_quota_check.test"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccQuotaCheckPreCheck(t) },
		CheckDestroy:             testAccCheckQuotaAndBucketDestroy,
		Steps: []resource.TestStep{
			{
				// The quota check needs an authoritative usage figure, which a
				// brand-new bucket does not have yet: write one object and wait
				// for the quota subsystem to publish it before reading the check.
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name          = %q
  force_destroy = true
}

resource "rustfs_quota" "test" {
  bucket     = rustfs_bucket.test.name
  quota      = 1048576
  depends_on = [rustfs_bucket.test]
}
`, bucketName),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccWriteQuotaObject(bucketName),
					waitForQuotaUsage(t, bucketName),
				),
			},
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name          = %q
  force_destroy = true
}

resource "rustfs_quota" "test" {
  bucket     = rustfs_bucket.test.name
  quota      = 1048576
  depends_on = [rustfs_bucket.test]
}

data "rustfs_quota_check" "test" {
  bucket         = rustfs_quota.test.bucket
  operation_type = "PUT"
  operation_size = 1024
  depends_on     = [rustfs_quota.test]
}
`, bucketName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(dataSourceName, "id", bucketName+"/PUT/1024"),
					resource.TestCheckResourceAttr(dataSourceName, "operation_type", "PUT"),
					resource.TestCheckResourceAttr(dataSourceName, "operation_size", "1024"),
					resource.TestCheckResourceAttr(dataSourceName, "allowed", "true"),
					resource.TestCheckResourceAttr(dataSourceName, "quota_limit", "1048576"),
					resource.TestCheckResourceAttrSet(dataSourceName, "remaining_quota"),
				),
			},
		},
	})
}

// testCheckQuotaStatsUsageNonZero asserts the data source's current_usage is a
// non-zero number, not merely set.
func testCheckQuotaStatsUsageNonZero(name string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		rs, ok := state.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("data source %s not found in state", name)
		}
		usage, ok := rs.Primary.Attributes["current_usage"]
		if !ok {
			return fmt.Errorf("current_usage is not set on %s", name)
		}
		if usage == "" || usage == "0" {
			return fmt.Errorf("current_usage = %q, want a non-zero value", usage)
		}
		return nil
	}
}

// testCheckAttributeNull asserts an attribute is null, which Terraform records
// by omitting it from the flat state.
func testCheckAttributeNull(name, attribute string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		rs, ok := state.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("resource %s not found in state", name)
		}
		if value, ok := rs.Primary.Attributes[attribute]; ok && value != "" {
			return fmt.Errorf("%s.%s = %q, want null", name, attribute, value)
		}
		return nil
	}
}
