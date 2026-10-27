// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const objectDataCacheStatsTestPayload = `{"mode":"memory","disabled":false,"entries":12,"lookups":100,` +
	`"hits":70,"fills":30,"invalidations":4,"inflight_fills":1,"singleflight_joins":5,` +
	`"memory_pressure_events":2}`

func TestObjectDataCacheStatsDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewObjectDataCacheStatsDataSource())

	required := []string{
		"id", "json", "mode", "disabled", "entries", "lookups", "hits", "fills", "invalidations",
		"inflight_fills", "singleflight_joins", "memory_pressure_events",
	}
	for _, name := range required {
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
}

func TestObjectDataCacheStatsDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewObjectDataCacheStatsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_object_data_cache_stats" {
		t.Errorf("expected rustfs_object_data_cache_stats, got %s", resp.TypeName)
	}
}

func TestObjectDataCacheStatsDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewObjectDataCacheStatsDataSource().(*ObjectDataCacheStatsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewObjectDataCacheStatsDataSource().(*ObjectDataCacheStatsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewObjectDataCacheStatsDataSource().(*ObjectDataCacheStatsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestObjectDataCacheStatsDataSourceRead(t *testing.T) {
	t.Run("surfaces the counters", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, objectDataCacheStatsTestPayload)
		d := NewObjectDataCacheStatsDataSource().(*ObjectDataCacheStatsDataSource)
		d.client = newUnitAllClient(srv)

		resp := readObjectDataCacheStats(t, d, dataSourceSchemaOf(t, d))
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/object-data-cache/stats" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got ObjectDataCacheStatsDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ID.ValueString() != "object-data-cache-stats" {
			t.Errorf("id = %q", got.ID.ValueString())
		}
		if got.JSON.ValueString() != objectDataCacheStatsTestPayload {
			t.Error("json does not carry the server body")
		}
		if got.Mode.ValueString() != "memory" || got.Disabled.ValueBool() {
			t.Errorf("mode = %q disabled=%v", got.Mode.ValueString(), got.Disabled.ValueBool())
		}
		if got.Entries.ValueInt64() != 12 || got.Lookups.ValueInt64() != 100 || got.Hits.ValueInt64() != 70 {
			t.Errorf("counters = %+v", got)
		}
		if got.Fills.ValueInt64() != 30 || got.Invalidations.ValueInt64() != 4 {
			t.Errorf("counters = %+v", got)
		}
		if got.InflightFills.ValueInt64() != 1 || got.SingleflightJoins.ValueInt64() != 5 {
			t.Errorf("counters = %+v", got)
		}
		if got.MemoryPressureEvents.ValueInt64() != 2 {
			t.Errorf("memory_pressure_events = %d", got.MemoryPressureEvents.ValueInt64())
		}
	})

	t.Run("explains a capability gap with the server version", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rustfs/admin/v3/object-data-cache/stats":
				w.WriteHeader(http.StatusNotImplemented)
				_, _ = io.WriteString(w, `<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`)
			case "/rustfs/admin/v3/healthinfo":
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.3"}`)
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		t.Cleanup(srv.Close)

		d := NewObjectDataCacheStatsDataSource().(*ObjectDataCacheStatsDataSource)
		d.client = newUnitAllClient(srv)
		resp := readObjectDataCacheStats(t, d, dataSourceSchemaOf(t, d))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Object data cache statistics are not available on this RustFS server")
		for _, want := range []string{"RustFS 1.0.3", "GET /rustfs/admin/v3/object-data-cache/stats", "Upgrade RustFS"} {
			if !strings.Contains(diagnostic.Detail(), want) {
				t.Errorf("detail %q does not contain %q", diagnostic.Detail(), want)
			}
		}
	})

	t.Run("reports an unexpected error without calling it a capability gap", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, `<Error><Code>InternalError</Code><Message>boom</Message></Error>`)
		d := NewObjectDataCacheStatsDataSource().(*ObjectDataCacheStatsDataSource)
		d.client = newUnitAllClient(srv)
		resp := readObjectDataCacheStats(t, d, dataSourceSchemaOf(t, d))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading object data cache statistics")
		if !strings.Contains(diagnostic.Detail(), "boom") {
			t.Errorf("detail %q does not carry the server payload", diagnostic.Detail())
		}
		if strings.Contains(diagnostic.Detail(), "not available") {
			t.Errorf("a server error must not be reported as a capability gap: %q", diagnostic.Detail())
		}
	})
}

func readObjectDataCacheStats(t *testing.T, d *ObjectDataCacheStatsDataSource, sch datasourceschema.Schema) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, ObjectDataCacheStatsDataSourceModel{ID: types.StringNull()}),
	}, resp)
	return resp
}

func TestAccObjectDataCacheStatsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccObjectDataCacheStatsPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_object_data_cache_stats" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_object_data_cache_stats.test", "id", "object-data-cache-stats"),
					resource.TestCheckResourceAttrSet("data.rustfs_object_data_cache_stats.test", "json"),
					resource.TestCheckResourceAttrSet("data.rustfs_object_data_cache_stats.test", "mode"),
				),
			},
		},
	})
}

func testAccObjectDataCacheStatsPreCheck(t *testing.T) {
	testAccPreCheck(t)
	client := testAccRustClient()
	if _, err := client.GetObjectDataCacheStats(context.Background()); err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("object data cache statistics are not implemented by the running RustFS, skipping: %v", err)
		}
		t.Fatalf("could not read object data cache statistics: %v", err)
	}
}
