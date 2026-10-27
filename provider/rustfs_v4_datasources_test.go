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
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestV4DataSourceMetadata(t *testing.T) {
	tests := []struct {
		want string
		d    datasource.DataSource
	}{
		{"rustfs_plugins", NewPluginsDataSource()},
		{"rustfs_extensions", NewExtensionsDataSource()},
		{"rustfs_cluster_snapshot", NewClusterSnapshotDataSource()},
	}
	for _, tt := range tests {
		resp := &datasource.MetadataResponse{}
		tt.d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
		if resp.TypeName != tt.want {
			t.Errorf("Metadata() = %q, want %q", resp.TypeName, tt.want)
		}
	}
}

func TestV4DataSourceConfigure(t *testing.T) {
	type configurable struct {
		configure func(context.Context, datasource.ConfigureRequest, *datasource.ConfigureResponse)
		client    func() *AllClient
	}
	tests := []struct {
		name string
		new  func() configurable
	}{
		{
			name: "plugins",
			new: func() configurable {
				d := NewPluginsDataSource().(*PluginsDataSource)
				return configurable{configure: d.Configure, client: func() *AllClient { return d.client }}
			},
		},
		{
			name: "extensions",
			new: func() configurable {
				d := NewExtensionsDataSource().(*ExtensionsDataSource)
				return configurable{configure: d.Configure, client: func() *AllClient { return d.client }}
			},
		},
		{
			name: "cluster snapshot",
			new: func() configurable {
				d := NewClusterSnapshotDataSource().(*ClusterSnapshotDataSource)
				return configurable{configure: d.Configure, client: func() *AllClient { return d.client }}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" nil provider data", func(t *testing.T) {
			d := tt.new()
			resp := &datasource.ConfigureResponse{}
			d.configure(context.Background(), datasource.ConfigureRequest{}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
			if client := d.client(); client != nil {
				t.Error("client must stay unset without provider data")
			}
		})

		t.Run(tt.name+" unexpected provider data", func(t *testing.T) {
			d := tt.new()
			resp := &datasource.ConfigureResponse{}
			d.configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
			assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
		})

		t.Run(tt.name+" all client", func(t *testing.T) {
			srv, _ := newRecorderServer(t, http.StatusOK, "")
			d := tt.new()
			resp := &datasource.ConfigureResponse{}
			d.configure(context.Background(), datasource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
			if client := d.client(); client == nil {
				t.Error("client was not set")
			}
		})
	}
}

func TestV4DataSourceSchemas(t *testing.T) {
	tests := []struct {
		d      datasource.DataSource
		fields []string
	}{
		{NewPluginsDataSource(), []string{"id", "catalog", "instances"}},
		{NewExtensionsDataSource(), []string{"id", "catalog", "instances"}},
		{NewClusterSnapshotDataSource(), []string{"id", "json"}},
	}
	for _, tt := range tests {
		sch := dataSourceSchemaOf(t, tt.d)
		for _, name := range tt.fields {
			attr, ok := sch.Attributes[name]
			if !ok {
				t.Errorf("%T is missing %q", tt.d, name)
				continue
			}
			if !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
				t.Errorf("%T attribute %q must be Computed only", tt.d, name)
			}
			if attr.GetDescription() == "" {
				t.Errorf("%T attribute %q has no description", tt.d, name)
			}
		}
	}
}

func TestPluginsDataSourceRead(t *testing.T) {
	const catalog = `{"plugins":[],"admin_discovery":{}}`
	const instances = `{"instances":[]}`

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/rustfs/admin/v4/plugins/catalog":
			_, _ = io.WriteString(w, catalog)
		case "/rustfs/admin/v4/plugins/instances":
			_, _ = io.WriteString(w, instances)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	d := NewPluginsDataSource().(*PluginsDataSource)
	d.client = newUnitAllClient(srv)

	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, PluginsDataSourceModel{
			ID:        types.StringNull(),
			Catalog:   types.StringNull(),
			Instances: types.StringNull(),
		}),
	}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var got PluginsDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ID.ValueString() != "plugins" || got.Catalog.ValueString() != catalog || got.Instances.ValueString() != instances {
		t.Errorf("state = %+v", got)
	}
	want := []string{"/rustfs/admin/v4/plugins/catalog", "/rustfs/admin/v4/plugins/instances"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestExtensionsDataSourceRead(t *testing.T) {
	const catalog = `{"extensions":[]}`
	const instances = `{"instances":[]}`

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/rustfs/admin/v4/extensions/catalog":
			_, _ = io.WriteString(w, catalog)
		case "/rustfs/admin/v4/extensions/instances":
			_, _ = io.WriteString(w, instances)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	d := NewExtensionsDataSource().(*ExtensionsDataSource)
	d.client = newUnitAllClient(srv)

	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, ExtensionsDataSourceModel{
			ID:        types.StringNull(),
			Catalog:   types.StringNull(),
			Instances: types.StringNull(),
		}),
	}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var got ExtensionsDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ID.ValueString() != "extensions" || got.Catalog.ValueString() != catalog || got.Instances.ValueString() != instances {
		t.Errorf("state = %+v", got)
	}
}

func TestClusterSnapshotDataSourceRead(t *testing.T) {
	const snapshot = `{"snapshot":null}`
	srv, recorder := newRecorderServer(t, http.StatusOK, snapshot)
	d := NewClusterSnapshotDataSource().(*ClusterSnapshotDataSource)
	d.client = newUnitAllClient(srv)

	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, ClusterSnapshotDataSourceModel{
			ID:   types.StringNull(),
			JSON: types.StringNull(),
		}),
	}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	request := recorder.only(t)
	if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v4/cluster/snapshot" {
		t.Errorf("unexpected request %s %s", request.Method, request.Path)
	}

	var got ClusterSnapshotDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ID.ValueString() != "cluster-snapshot" || got.JSON.ValueString() != snapshot {
		t.Errorf("state = %+v", got)
	}
}

func TestPluginsDataSourceReadUpgradeRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rustfs/admin/v4/plugins/catalog":
			w.WriteHeader(http.StatusUpgradeRequired)
		case "/rustfs/admin/v3/healthinfo":
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.2"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	d := NewPluginsDataSource().(*PluginsDataSource)
	d.client = newUnitAllClient(srv)

	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, PluginsDataSourceModel{
			ID:        types.StringNull(),
			Catalog:   types.StringNull(),
			Instances: types.StringNull(),
		}),
	}, resp)

	diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Plugins are not available on this RustFS server")
	for _, want := range []string{
		"does not serve GET /rustfs/admin/v4/plugins/catalog",
		"upgrade RustFS or use the v3 route",
	} {
		if !strings.Contains(diagnostic.Detail(), want) {
			t.Errorf("detail %q does not contain %q", diagnostic.Detail(), want)
		}
	}
}

func TestExtensionsDataSourceReadNotImplemented(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rustfs/admin/v4/extensions/catalog":
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, `<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`)
		case "/rustfs/admin/v3/healthinfo":
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.3"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	d := NewExtensionsDataSource().(*ExtensionsDataSource)
	d.client = newUnitAllClient(srv)

	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, ExtensionsDataSourceModel{
			ID:        types.StringNull(),
			Catalog:   types.StringNull(),
			Instances: types.StringNull(),
		}),
	}, resp)

	diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Extensions are not available on this RustFS server")
	if !strings.Contains(diagnostic.Detail(), "does not implement GET /rustfs/admin/v4/extensions/catalog") {
		t.Errorf("detail = %q", diagnostic.Detail())
	}
}

func TestClusterSnapshotDataSourceReadUpgradeRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rustfs/admin/v4/cluster/snapshot":
			w.WriteHeader(http.StatusUpgradeRequired)
		case "/rustfs/admin/v3/healthinfo":
			_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.2"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	d := NewClusterSnapshotDataSource().(*ClusterSnapshotDataSource)
	d.client = newUnitAllClient(srv)

	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, ClusterSnapshotDataSourceModel{
			ID:   types.StringNull(),
			JSON: types.StringNull(),
		}),
	}, resp)

	diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Cluster snapshot is not available on this RustFS server")
	for _, want := range []string{
		"does not serve GET /rustfs/admin/v4/cluster/snapshot",
		"upgrade RustFS or use the v3 route",
	} {
		if !strings.Contains(diagnostic.Detail(), want) {
			t.Errorf("detail %q does not contain %q", diagnostic.Detail(), want)
		}
	}
}

func TestClusterSnapshotDataSourceReadUnexpectedError(t *testing.T) {
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	d := NewClusterSnapshotDataSource().(*ClusterSnapshotDataSource)
	d.client = newUnitAllClient(srv)

	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, ClusterSnapshotDataSourceModel{
			ID:   types.StringNull(),
			JSON: types.StringNull(),
		}),
	}, resp)

	diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading cluster snapshot")
	if !strings.Contains(diagnostic.Detail(), "boom") {
		t.Errorf("detail %q does not carry the server payload", diagnostic.Detail())
	}
	if strings.Contains(diagnostic.Detail(), "not available") {
		t.Errorf("a server error must not be reported as a capability gap: %q", diagnostic.Detail())
	}
}

func TestAccPluginsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccV4Readable(t, func() error {
				client := testAccRustClient()
				_, err := client.GetPluginCatalog(context.Background())
				return err
			})
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_plugins" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_plugins.test", "id", "plugins"),
					resource.TestCheckResourceAttrSet("data.rustfs_plugins.test", "catalog"),
					resource.TestCheckResourceAttrSet("data.rustfs_plugins.test", "instances"),
				),
			},
		},
	})
}

func TestAccExtensionsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccV4Readable(t, func() error {
				client := testAccRustClient()
				_, err := client.GetExtensionCatalog(context.Background())
				return err
			})
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_extensions" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_extensions.test", "id", "extensions"),
					resource.TestCheckResourceAttrSet("data.rustfs_extensions.test", "catalog"),
					resource.TestCheckResourceAttrSet("data.rustfs_extensions.test", "instances"),
				),
			},
		},
	})
}

func TestAccClusterSnapshotDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccV4Readable(t, func() error {
				client := testAccRustClient()
				_, err := client.GetClusterSnapshot(context.Background())
				return err
			})
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_cluster_snapshot" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_cluster_snapshot.test", "id", "cluster-snapshot"),
					resource.TestCheckResourceAttrSet("data.rustfs_cluster_snapshot.test", "json"),
				),
			},
		},
	})
}

func testAccV4Readable(t *testing.T, read func() error) {
	t.Helper()

	testAccPreCheck(t)
	if err := read(); err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("the v4 admin route is not served by the running RustFS, skipping: %v", err)
		}
		t.Fatalf("could not read the v4 admin route: %v", err)
	}
}
