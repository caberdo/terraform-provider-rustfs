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
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const runtimeCapabilitiesTestPayload = `{"storage_classes":{"contract_version":1,` +
	`"supported_write_classes":["STANDARD","REDUCED_REDUNDANCY"],` +
	`"unsupported_write_error":"InvalidStorageClass",` +
	`"legacy_label_behavior":"normalized_to_effective_class"}}`

func TestRuntimeCapabilitiesDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewRuntimeCapabilitiesDataSource())

	for _, name := range []string{"id", "json", "storage_classes"} {
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

	storageClasses, ok := sch.Attributes["storage_classes"].(datasourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("storage_classes is %T, want SingleNestedAttribute", sch.Attributes["storage_classes"])
	}
	for _, name := range []string{"contract_version", "supported_write_classes", "unsupported_write_error", "legacy_label_behavior"} {
		if _, ok := storageClasses.Attributes[name]; !ok {
			t.Errorf("storage_classes is missing %q", name)
		}
	}
}

func TestRuntimeCapabilitiesDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewRuntimeCapabilitiesDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_runtime_capabilities" {
		t.Errorf("expected rustfs_runtime_capabilities, got %s", resp.TypeName)
	}
}

func TestRuntimeCapabilitiesDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewRuntimeCapabilitiesDataSource().(*RuntimeCapabilitiesDataSource)
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
		d := NewRuntimeCapabilitiesDataSource().(*RuntimeCapabilitiesDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewRuntimeCapabilitiesDataSource().(*RuntimeCapabilitiesDataSource)
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

func TestRuntimeCapabilitiesDataSourceRead(t *testing.T) {
	t.Run("surfaces the storage-class contract", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, runtimeCapabilitiesTestPayload)
		d := NewRuntimeCapabilitiesDataSource().(*RuntimeCapabilitiesDataSource)
		d.client = newUnitAllClient(srv)

		resp := readRuntimeCapabilities(t, d)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v4/runtime/capabilities" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got RuntimeCapabilitiesDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ID.ValueString() != "runtime-capabilities" {
			t.Errorf("id = %q", got.ID.ValueString())
		}
		if got.JSON.ValueString() != runtimeCapabilitiesTestPayload {
			t.Error("json does not carry the server body")
		}

		var classes runtimeStorageClassModel
		if diags := got.StorageClasses.As(context.Background(), &classes, basetypes.ObjectAsOptions{}); diags.HasError() {
			t.Fatalf("reading storage_classes: %v", diags)
		}
		if classes.ContractVersion.ValueInt64() != 1 {
			t.Errorf("contract_version = %d", classes.ContractVersion.ValueInt64())
		}
		if classes.UnsupportedWriteError.ValueString() != "InvalidStorageClass" {
			t.Errorf("unsupported_write_error = %q", classes.UnsupportedWriteError.ValueString())
		}
		if classes.LegacyLabelBehavior.ValueString() != "normalized_to_effective_class" {
			t.Errorf("legacy_label_behavior = %q", classes.LegacyLabelBehavior.ValueString())
		}
		var writeClasses []string
		if diags := classes.SupportedWriteClasses.ElementsAs(context.Background(), &writeClasses, false); diags.HasError() {
			t.Fatalf("reading supported_write_classes: %v", diags)
		}
		if strings.Join(writeClasses, ",") != "STANDARD,REDUCED_REDUNDANCY" {
			t.Errorf("supported_write_classes = %v", writeClasses)
		}
	})

	t.Run("explains a 511 with the route and the server version", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rustfs/admin/v4/runtime/capabilities":
				w.WriteHeader(http.StatusUpgradeRequired)
			case "/rustfs/admin/v3/healthinfo":
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.2"}`)
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		t.Cleanup(srv.Close)

		d := NewRuntimeCapabilitiesDataSource().(*RuntimeCapabilitiesDataSource)
		d.client = newUnitAllClient(srv)
		resp := readRuntimeCapabilities(t, d)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Runtime capabilities are not available on this RustFS server")
		for _, want := range []string{
			"RustFS 1.0.2",
			"does not serve GET /rustfs/admin/v4/runtime/capabilities",
			"upgrade RustFS or use the v3 route",
		} {
			if !strings.Contains(diagnostic.Detail(), want) {
				t.Errorf("detail %q does not contain %q", diagnostic.Detail(), want)
			}
		}
	})

	t.Run("reports an unexpected error without calling it a capability gap", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
		d := NewRuntimeCapabilitiesDataSource().(*RuntimeCapabilitiesDataSource)
		d.client = newUnitAllClient(srv)
		resp := readRuntimeCapabilities(t, d)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading runtime capabilities")
		if !strings.Contains(diagnostic.Detail(), "boom") {
			t.Errorf("detail %q does not carry the server payload", diagnostic.Detail())
		}
		if strings.Contains(diagnostic.Detail(), "not available") {
			t.Errorf("a server error must not be reported as a capability gap: %q", diagnostic.Detail())
		}
	})
}

func readRuntimeCapabilities(t *testing.T, d *RuntimeCapabilitiesDataSource) *datasource.ReadResponse {
	t.Helper()

	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, RuntimeCapabilitiesDataSourceModel{
			ID:             types.StringNull(),
			JSON:           types.StringNull(),
			StorageClasses: types.ObjectNull(runtimeStorageClassAttrTypes),
		}),
	}, resp)
	return resp
}

func TestAccRuntimeCapabilitiesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccV4PreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_runtime_capabilities" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_runtime_capabilities.test", "id", "runtime-capabilities"),
					resource.TestCheckResourceAttrSet("data.rustfs_runtime_capabilities.test", "json"),
					resource.TestCheckResourceAttrSet("data.rustfs_runtime_capabilities.test", "storage_classes.contract_version"),
				),
			},
		},
	})
}

func testAccV4PreCheck(t *testing.T) {
	testAccPreCheck(t)
	client := testAccRustClient()
	if _, err := client.GetRuntimeCapabilities(context.Background()); err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("the v4 admin API is not served by the running RustFS, skipping: %v", err)
		}
		t.Fatalf("could not read runtime capabilities: %v", err)
	}
}
