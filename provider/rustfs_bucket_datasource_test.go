// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	listBucketsPayload = `<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Owner><ID>rustfs</ID></Owner>
<Buckets>
<Bucket><Name>zeta</Name><CreationDate>2026-01-04T11:02:03.000Z</CreationDate></Bucket>
<Bucket><Name>alpha</Name><CreationDate>2025-12-31T08:00:00.000Z</CreationDate></Bucket>
</Buckets>
</ListAllMyBucketsResult>`

	bucketLocationPayload = `<?xml version="1.0" encoding="UTF-8"?>
<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">eu-west-1</LocationConstraint>`
)

func TestBucketDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewBucketDataSource())

	attrs := sch.GetAttributes()
	if len(attrs) != 5 {
		t.Errorf("expected 5 attributes, got %d: %v", len(attrs), attrs)
	}
	if name, ok := attrs["name"].(datasourceschema.StringAttribute); !ok || !name.Required {
		t.Errorf("name must be a required string attribute")
	}
	for _, computed := range []string{"id", "exists", "creation_date", "region"} {
		attr, ok := attrs[computed].(datasourceschema.StringAttribute)
		if computed == "exists" {
			b, isBool := attrs[computed].(datasourceschema.BoolAttribute)
			if !isBool || !b.Computed {
				t.Errorf("%s must be a computed bool attribute", computed)
			}
			continue
		}
		if !ok || !attr.Computed {
			t.Errorf("%s must be a computed string attribute", computed)
		}
	}
	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description, tfplugindocs would publish an empty row", name)
		}
	}
}

func TestBucketDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewBucketDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket" {
		t.Errorf("expected rustfs_bucket, got %s", resp.TypeName)
	}
}

func TestBucketDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewBucketDataSource().(*bucketDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewBucketDataSource().(*bucketDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})
}

// bucketLookupServer answers the three S3 calls the data source can make:
// HeadBucket, ListBuckets and GetBucketLocation.
type bucketLookupServer struct {
	t *testing.T

	headStatus     int
	listStatus     int
	listBody       string
	locationStatus int
	locationBody   string
}

func (s *bucketLookupServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case r.Method == http.MethodHead:
		status := s.headStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	case r.Method == http.MethodGet && r.URL.Path == "/":
		status := s.listStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		body := s.listBody
		if body == "" {
			body = listBucketsPayload
		}
		fmt.Fprint(w, body)
	case r.Method == http.MethodGet && r.URL.Query().Has("location"):
		status := s.locationStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		body := s.locationBody
		if body == "" {
			body = bucketLocationPayload
		}
		fmt.Fprint(w, body)
	default:
		s.t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, s3ErrorXML("UnroutedRequest", "no route"))
	}
}

func (d *bucketDataSource) readForTest(t *testing.T, model bucketDataSourceModel) *datasource.ReadResponse {
	t.Helper()
	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func TestBucketDataSourceRead(t *testing.T) {
	t.Run("reports an existing bucket", func(t *testing.T) {
		d := NewBucketDataSource().(*bucketDataSource)
		d.client = httptestClient(t, &bucketLookupServer{t: t})

		resp := d.readForTest(t, bucketDataSourceModel{Name: types.StringValue("alpha")})
		requireNoDiagnostics(t, resp.Diagnostics)

		var got bucketDataSourceModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
		if got.Id.ValueString() != "alpha" {
			t.Errorf("id = %q, want alpha", got.Id.ValueString())
		}
		if !got.Exists.ValueBool() {
			t.Error("exists = false, want true")
		}
		if got.CreationDate.ValueString() != "2025-12-31T08:00:00Z" {
			t.Errorf("creation_date = %q", got.CreationDate.ValueString())
		}
		if got.Region.ValueString() != "eu-west-1" {
			t.Errorf("region = %q, want eu-west-1", got.Region.ValueString())
		}
	})

	t.Run("a missing bucket reports exists false without an error", func(t *testing.T) {
		d := NewBucketDataSource().(*bucketDataSource)
		d.client = httptestClient(t, &bucketLookupServer{
			t:          t,
			headStatus: http.StatusNotFound,
		})

		resp := d.readForTest(t, bucketDataSourceModel{Name: types.StringValue("absent")})
		requireNoDiagnostics(t, resp.Diagnostics)

		var got bucketDataSourceModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
		if got.Exists.ValueBool() {
			t.Error("exists = true, want false")
		}
		if !got.CreationDate.IsNull() || !got.Region.IsNull() {
			t.Errorf("expected null creation_date and region, got %v / %v", got.CreationDate, got.Region)
		}
	})

	t.Run("an authorization failure is named", func(t *testing.T) {
		d := NewBucketDataSource().(*bucketDataSource)
		d.client = httptestClient(t, &bucketLookupServer{
			t:          t,
			headStatus: http.StatusForbidden,
		})

		resp := d.readForTest(t, bucketDataSourceModel{Name: types.StringValue("secret")})
		assertErrorDiagnostic(t, resp.Diagnostics, "Error looking up bucket", "s3:ListBucket")
	})

	t.Run("a failed bucket listing names the permission", func(t *testing.T) {
		d := NewBucketDataSource().(*bucketDataSource)
		d.client = httptestClient(t, &bucketLookupServer{
			t:          t,
			listStatus: http.StatusForbidden,
			listBody:   s3ErrorXML("AccessDenied", "no list"),
		})

		resp := d.readForTest(t, bucketDataSourceModel{Name: types.StringValue("alpha")})
		assertErrorDiagnostic(t, resp.Diagnostics, "Error listing buckets", "s3:ListAllMyBuckets")
	})

	t.Run("an unimplemented location lookup leaves the region empty", func(t *testing.T) {
		d := NewBucketDataSource().(*bucketDataSource)
		d.client = httptestClient(t, &bucketLookupServer{
			t:              t,
			locationStatus: http.StatusNotImplemented,
			locationBody:   s3ErrorXML("NotImplemented", "no location here"),
		})

		resp := d.readForTest(t, bucketDataSourceModel{Name: types.StringValue("alpha")})
		requireNoDiagnostics(t, resp.Diagnostics)

		var got bucketDataSourceModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
		if got.Region.ValueString() != "" {
			t.Errorf("region = %q, want empty", got.Region.ValueString())
		}
	})
}

func TestBucketsDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewBucketsDataSource())

	attrs := sch.GetAttributes()
	if len(attrs) != 1 {
		t.Errorf("expected 1 attribute, got %d: %v", len(attrs), attrs)
	}
	buckets, ok := attrs["buckets"].(datasourceschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("buckets is not a ListNestedAttribute")
	}
	if !buckets.Computed {
		t.Error("buckets must be computed")
	}
	for name, attr := range buckets.NestedObject.Attributes {
		if attr.GetDescription() == "" {
			t.Errorf("nested attribute %s has no description", name)
		}
	}
}

func TestBucketsDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewBucketsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_buckets" {
		t.Errorf("expected rustfs_buckets, got %s", resp.TypeName)
	}
}

func TestBucketsDataSourceRead(t *testing.T) {
	readBuckets := func(t *testing.T, server http.Handler, model bucketsDataSourceModel) *datasource.ReadResponse {
		t.Helper()
		d := NewBucketsDataSource().(*bucketsDataSource)
		d.client = httptestClient(t, server)
		sch := dataSourceSchemaOf(t, d)
		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, model),
		}, resp)
		return resp
	}

	t.Run("sorts the server's response by name", func(t *testing.T) {
		resp := readBuckets(t, &bucketLookupServer{t: t}, bucketsDataSourceModel{
			Buckets: types.ListNull(types.ObjectType{AttrTypes: bucketSummaryAttrTypes}),
		})
		requireNoDiagnostics(t, resp.Diagnostics)

		var got bucketsDataSourceModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))

		var summaries []bucketSummaryModel
		requireNoDiagnostics(t, got.Buckets.ElementsAs(context.Background(), &summaries, false))
		if len(summaries) != 2 {
			t.Fatalf("expected 2 buckets, got %d", len(summaries))
		}
		if summaries[0].Name.ValueString() != "alpha" || summaries[1].Name.ValueString() != "zeta" {
			t.Errorf("buckets are not sorted by name: %v, %v", summaries[0].Name, summaries[1].Name)
		}
		if summaries[0].CreationDate.ValueString() != "2025-12-31T08:00:00Z" {
			t.Errorf("creation_date = %q", summaries[0].CreationDate.ValueString())
		}
	})

	t.Run("a failed listing is an error, never an empty list", func(t *testing.T) {
		resp := readBuckets(t, &bucketLookupServer{
			t:          t,
			listStatus: http.StatusForbidden,
			listBody:   s3ErrorXML("AccessDenied", "no list"),
		}, bucketsDataSourceModel{
			Buckets: types.ListNull(types.ObjectType{AttrTypes: bucketSummaryAttrTypes}),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error listing buckets", "s3:ListAllMyBuckets")
	})
}

// TestAccBucketDataSources reads a live bucket back through both data sources
// and asserts the missing-bucket path plans cleanly.
func TestAccBucketDataSources(t *testing.T) {
	name := fmt.Sprintf("tf-test-bucket-ds-%d", acctest.RandInt())
	resourceName := "data.rustfs_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketDataSourcesConfig(name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", name),
					resource.TestCheckResourceAttr(resourceName, "id", name),
					resource.TestCheckResourceAttr(resourceName, "exists", "true"),
					resource.TestCheckResourceAttrSet(resourceName, "creation_date"),
					resource.TestCheckResourceAttr("data.rustfs_bucket.missing", "exists", "false"),
					testAccCheckBucketListed(name),
				),
			},
		},
	})
}

func testAccBucketDataSourcesConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

data "rustfs_bucket" "test" {
  name       = rustfs_bucket.test.name
  depends_on = [rustfs_bucket.test]
}

data "rustfs_bucket" "missing" {
  name = "%s-absent"
}

data "rustfs_buckets" "all" {
  depends_on = [rustfs_bucket.test]
}
`, name, name)
}

// testAccCheckBucketListed asserts the bucket appears in rustfs_buckets and
// that the provider sorted the list by name.
func testAccCheckBucketListed(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["data.rustfs_buckets.all"]
		if !ok {
			return fmt.Errorf("data.rustfs_buckets.all not found in state")
		}

		count, err := strconv.Atoi(rs.Primary.Attributes["buckets.#"])
		if err != nil {
			return fmt.Errorf("reading bucket count: %w", err)
		}

		found := false
		previous := ""
		for i := range count {
			current := rs.Primary.Attributes[fmt.Sprintf("buckets.%d.name", i)]
			if current < previous {
				return fmt.Errorf("buckets are not sorted: %q comes after %q", current, previous)
			}
			previous = current
			if current == name {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("bucket %s is missing from rustfs_buckets", name)
		}
		return nil
	}
}

func TestBucketsDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewBucketsDataSource().(*bucketsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewBucketsDataSource().(*bucketsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})
}

func TestBucketDataSourceReadBucketMissingFromListing(t *testing.T) {
	d := NewBucketDataSource().(*bucketDataSource)
	d.client = httptestClient(t, &bucketLookupServer{t: t, listBody: listBucketsPayload})

	resp := d.readForTest(t, bucketDataSourceModel{Name: types.StringValue("not-in-the-list")})
	requireNoDiagnostics(t, resp.Diagnostics)

	var got bucketDataSourceModel
	requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
	if !got.Exists.ValueBool() {
		t.Error("exists = false, want true (HeadBucket succeeded)")
	}
	if !got.CreationDate.IsNull() {
		t.Errorf("creation_date = %v, want null when the listing omits the bucket", got.CreationDate)
	}
}
