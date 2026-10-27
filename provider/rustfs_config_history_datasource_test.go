// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const configHistoryTestPayload = `[
	{"RestoreID":"4a1c","CreateTime":"2026-10-20T09:15:00Z","Data":"notify_webhook endpoint=\"*redacted*\"","DataRedacted":true},
	{"RestoreID":"3b2d","CreateTime":"2026-10-19T18:00:00Z","Data":"scanner delay=\"10\"","DataRedacted":true}
]`

func configHistoryConfig(count *int64) *ConfigHistoryDataSourceModel {
	model := &ConfigHistoryDataSourceModel{
		ID:         types.StringNull(),
		MaxEntries: types.Int64Null(),
		Entries:    types.ListNull(types.ObjectType{AttrTypes: configHistoryEntryAttrTypes}),
	}
	if count != nil {
		model.MaxEntries = types.Int64Value(*count)
	}
	return model
}

func readConfigHistory(t *testing.T, d *ConfigHistoryDataSource, sch datasourceschema.Schema, model *ConfigHistoryDataSourceModel) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func TestConfigHistoryDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewConfigHistoryDataSource())

	for _, name := range []string{"id", "entries"} {
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

	count, ok := sch.Attributes["max_entries"].(datasourceschema.Int64Attribute)
	if !ok || !count.Optional || !count.Computed {
		t.Errorf("max_entries must be Optional and Computed: %+v", sch.Attributes["max_entries"])
	}

	entries, ok := sch.Attributes["entries"].(datasourceschema.ListNestedAttribute)
	if !ok || !entries.Computed {
		t.Fatalf("entries must be a computed nested list: %+v", sch.Attributes["entries"])
	}
	data, ok := entries.NestedObject.Attributes["data"].(datasourceschema.StringAttribute)
	if !ok || !data.Sensitive || !data.Computed {
		t.Errorf("entry data must be Computed and Sensitive: %+v", entries.NestedObject.Attributes["data"])
	}
	for name, attr := range entries.NestedObject.Attributes {
		if attr.GetDescription() == "" {
			t.Errorf("nested attribute %q has no description", name)
		}
	}
}

func TestConfigHistoryDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewConfigHistoryDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_config_history" {
		t.Errorf("expected rustfs_config_history, got %s", resp.TypeName)
	}
}

func TestConfigHistoryDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewConfigHistoryDataSource().(*ConfigHistoryDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewConfigHistoryDataSource().(*ConfigHistoryDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewConfigHistoryDataSource().(*ConfigHistoryDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestConfigHistoryDataSourceRead(t *testing.T) {
	t.Run("lists the entries and honours the limit", func(t *testing.T) {
		var gotPath, gotQuery string
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(configHistoryTestPayload))
		}))
		d := NewConfigHistoryDataSource().(*ConfigHistoryDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		count := int64(7)
		resp := readConfigHistory(t, d, sch, configHistoryConfig(&count))
		requireNoDiagnostics(t, resp.Diagnostics)

		if gotPath != "/rustfs/admin/v3/list-config-history-kv" {
			t.Errorf("path = %q", gotPath)
		}
		if gotQuery != "count=7" {
			t.Errorf("query = %q, want count=7", gotQuery)
		}
		assertStateString(t, resp.State, path.Root("id"), "config-history/7")
		assertStateInt64(t, resp.State, path.Root("max_entries"), 7)

		var entries []ConfigHistoryEntryModel
		if diags := resp.State.GetAttribute(context.Background(), path.Root("entries"), &entries); diags.HasError() {
			t.Fatalf("reading entries: %v", diags)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
		if entries[0].RestoreID.ValueString() != "4a1c" || entries[0].CreateTime.ValueString() != "2026-10-20T09:15:00Z" {
			t.Errorf("unexpected first entry: %+v", entries[0])
		}
		if !strings.Contains(entries[0].Data.ValueString(), "*redacted*") || !entries[0].DataRedacted.ValueBool() {
			t.Errorf("unexpected redaction: %+v", entries[0])
		}
	})

	t.Run("defaults the count", func(t *testing.T) {
		var gotQuery string
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		}))
		d := NewConfigHistoryDataSource().(*ConfigHistoryDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readConfigHistory(t, d, sch, configHistoryConfig(nil))
		requireNoDiagnostics(t, resp.Diagnostics)

		if gotQuery != "count=100" {
			t.Errorf("query = %q, want count=100", gotQuery)
		}
		assertStateInt64(t, resp.State, path.Root("max_entries"), 100)

		var entries []ConfigHistoryEntryModel
		if diags := resp.State.GetAttribute(context.Background(), path.Root("entries"), &entries); diags.HasError() {
			t.Fatalf("reading entries: %v", diags)
		}
		if len(entries) != 0 {
			t.Errorf("expected no entries, got %+v", entries)
		}
	})

	t.Run("reports a server error", func(t *testing.T) {
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied by admin gate</Message></Error>`))
		}))
		d := NewConfigHistoryDataSource().(*ConfigHistoryDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readConfigHistory(t, d, sch, configHistoryConfig(nil))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading config history")
		if !strings.Contains(diagnostic.Detail(), "denied by admin gate") {
			t.Errorf("detail = %q", diagnostic.Detail())
		}
	})
}

func TestAccConfigHistoryDataSource(t *testing.T) {
	scope := fmt.Sprintf("notify_webhook:tfacchist%d", acctest.RandInt())

	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
	})
	_ = client.DeleteConfig(context.Background(), scope)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccPreCheck(t) },
		CheckDestroy:             testAccCheckConfigDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_config" "test" {
  sub_system = %q
  settings = {
    endpoint = "http://127.0.0.1:18080/rustfs/events"
  }
}

data "rustfs_config_history" "test" {
  max_entries = 10
  depends_on  = [rustfs_config.test]
}
`, scope),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_config_history.test", "max_entries", "10"),
					resource.TestCheckResourceAttrSet("data.rustfs_config_history.test", "entries.0.restore_id"),
					resource.TestCheckResourceAttr("data.rustfs_config_history.test", "entries.0.data_redacted", "true"),
				),
			},
		},
	})
}
