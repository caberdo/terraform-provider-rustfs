// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const poolsDataSourceName = "data.rustfs_pools.test"

// poolsDataSourcePayload is what GET /rustfs/admin/v3/pools/list returns on
// RustFS 1.0.3 for a single-pool deployment: it predates the 1.0.5
// diagnostics, so none of the new keys are present. The pool's command line is
// the name the data source exposes.
const poolsDataSourcePayload = `[{"id":0,"cmdline":"/data","lastUpdate":"2026-10-19T06:31:51.126435444Z",` +
	`"totalSize":63728975872,"currentSize":53833203712,"usedSize":9895772160,` +
	`"used":0.15527900809006742,"status":"active","decommissionStatus":"none",` +
	`"rebalanceStatus":"none","decommissionInfo":null}]`

// poolsDataSourcePayloadDiagnostics is a live 1.0.5 answer for the same
// deployment, including the five diagnostic keys rustfs/rustfs#8995 added.
const poolsDataSourcePayloadDiagnostics = `[{"id":0,"cmdline":"/data","lastUpdate":"2026-11-11T08:12:05.210883873Z",` +
	`"totalSize":63728975872,"currentSize":47960670208,"usedSize":15768305664,` +
	`"used":0.2474275703986006,"status":"active","decommissionStatus":"none",` +
	`"rebalanceStatus":"none","decommissionInfo":null,"availability":"online",` +
	`"poolMetadataStatus":"fresh","rebalanceMetadataStatus":"fresh","capacityStatus":"live",` +
	`"observedAt":"2026-11-11T08:19:27.821896967Z"}]`

// emptyPoolsModel is the config a read starts from: both computed lists unset.
func emptyPoolsModel() PoolsDataSourceModel {
	return PoolsDataSourceModel{
		Names: types.ListNull(types.StringType),
		Pools: types.ListNull(types.ObjectType{AttrTypes: poolDiagnosticsAttrTypes}),
	}
}

func TestPoolsDataSourceSchema(t *testing.T) {
	d := NewPoolsDataSource()
	sch := dataSourceSchemaOf(t, d)

	attrs := sch.GetAttributes()
	if len(attrs) != 2 {
		t.Errorf("expected 2 attributes, got %d: %v", len(attrs), attrs)
	}

	names, ok := attrs["names"].(datasourceschema.ListAttribute)
	if !ok {
		t.Fatalf("names is not a ListAttribute")
	}
	if !names.Computed || names.Optional || names.Required {
		t.Errorf("names must be Computed only: %+v", names)
	}
	if !names.ElementType.Equal(types.StringType) {
		t.Errorf("names must hold strings, got %s", names.ElementType)
	}

	pools, ok := attrs["pools"].(datasourceschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("pools is not a ListNestedAttribute")
	}
	if !pools.Computed || pools.Optional || pools.Required {
		t.Errorf("pools must be Computed only: %+v", pools)
	}

	wantNested := map[string]bool{
		"name": false, "status": false, "availability": false, "capacity_status": false,
		"pool_metadata_status": false, "rebalance_metadata_status": false, "observed_at": false,
	}
	nested := pools.NestedObject.GetAttributes()
	for name, attr := range nested {
		if _, ok := wantNested[name]; !ok {
			t.Errorf("unexpected nested attribute %s", name)
			continue
		}
		wantNested[name] = true
		str, ok := attr.(datasourceschema.StringAttribute)
		if !ok {
			t.Errorf("nested attribute %s is not a StringAttribute", name)
			continue
		}
		if !str.Computed || str.Optional || str.Required {
			t.Errorf("nested attribute %s must be Computed only: %+v", name, str)
		}
	}
	for name, seen := range wantNested {
		if !seen {
			t.Errorf("nested attribute %s is missing", name)
		}
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description, tfplugindocs would publish an empty row", name)
		}
	}
	for name, attr := range nested {
		if attr.GetDescription() == "" {
			t.Errorf("nested attribute %s has no description, tfplugindocs would publish an empty row", name)
		}
	}

	if !strings.Contains(sch.MarkdownDescription, "availability") {
		t.Errorf("the description must name the diagnostics it now delivers, got %q", sch.MarkdownDescription)
	}
}

func TestPoolsDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewPoolsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_pools" {
		t.Errorf("expected rustfs_pools, got %s", resp.TypeName)
	}
}

func TestPoolsDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewPoolsDataSource().(*PoolsDataSource)
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
		d := NewPoolsDataSource().(*PoolsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewPoolsDataSource().(*PoolsDataSource)
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

// readPools drives one Read against a canned server and returns the state.
func readPools(t *testing.T, status int, body string) (PoolsDataSourceModel, *datasource.ReadResponse) {
	t.Helper()
	srv, _ := newRecorderServer(t, status, body)
	d := NewPoolsDataSource().(*PoolsDataSource)
	d.client = newUnitAllClient(srv)
	sch := dataSourceSchemaOf(t, d)

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, emptyPoolsModel()),
	}, resp)

	var got PoolsDataSourceModel
	if !resp.Diagnostics.HasError() {
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
	}
	return got, resp
}

func TestPoolsDataSourceRead(t *testing.T) {
	t.Run("lists the pools", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, poolsDataSourcePayloadDiagnostics)
		d := NewPoolsDataSource().(*PoolsDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, emptyPoolsModel()),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/pools/list" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got PoolsDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Names.IsNull() || got.Names.IsUnknown() {
			t.Fatalf("names was not written to the state: %+v", got.Names)
		}
		var names []string
		if diags := got.Names.ElementsAs(context.Background(), &names, false); diags.HasError() {
			t.Fatalf("reading the pool names: %v", diags)
		}
		if len(names) != 1 || names[0] != "/data" {
			t.Errorf("expected the pool command line as the only name, got %v", names)
		}

		pools := poolsFromModel(t, got)
		if len(pools) != 1 {
			t.Fatalf("expected 1 pool entry, got %d", len(pools))
		}
		pool := pools[0]
		if pool.Name.ValueString() != "/data" || pool.Status.ValueString() != "active" {
			t.Errorf("unexpected name/status: %q/%q", pool.Name, pool.Status)
		}
		for name, value := range map[string]types.String{
			"availability":              pool.Availability,
			"capacity_status":           pool.CapacityStatus,
			"pool_metadata_status":      pool.PoolMetadataStatus,
			"rebalance_metadata_status": pool.RebalanceMetadataStatus,
			"observed_at":               pool.ObservedAt,
		} {
			if value.IsNull() || value.IsUnknown() {
				t.Errorf("%s was not written to the state: %+v", name, value)
			}
		}
		if pool.Availability.ValueString() != "online" {
			t.Errorf("expected online, got %q", pool.Availability.ValueString())
		}
		if pool.CapacityStatus.ValueString() != "live" {
			t.Errorf("expected live, got %q", pool.CapacityStatus.ValueString())
		}
		if pool.ObservedAt.ValueString() != "2026-11-11T08:19:27.821896967Z" {
			t.Errorf("unexpected observed_at: %q", pool.ObservedAt.ValueString())
		}
	})

	t.Run("an older server reports null diagnostics", func(t *testing.T) {
		got, resp := readPools(t, http.StatusOK, poolsDataSourcePayload)
		if resp.Diagnostics.HasError() {
			t.Fatalf("a pre-1.0.5 server must not fail the read: %v", resp.Diagnostics)
		}

		var names []string
		if diags := got.Names.ElementsAs(context.Background(), &names, false); diags.HasError() {
			t.Fatalf("reading the pool names: %v", diags)
		}
		if len(names) != 1 || names[0] != "/data" {
			t.Errorf("names must stay unchanged on an older server, got %v", names)
		}

		pools := poolsFromModel(t, got)
		if len(pools) != 1 {
			t.Fatalf("expected 1 pool entry, got %d", len(pools))
		}
		if pools[0].Name.ValueString() != "/data" {
			t.Errorf("unexpected pool name: %q", pools[0].Name.ValueString())
		}
		for name, value := range map[string]types.String{
			"availability":              pools[0].Availability,
			"capacity_status":           pools[0].CapacityStatus,
			"pool_metadata_status":      pools[0].PoolMetadataStatus,
			"rebalance_metadata_status": pools[0].RebalanceMetadataStatus,
			"observed_at":               pools[0].ObservedAt,
		} {
			if !value.IsNull() {
				t.Errorf("%s must be null when the server does not send it, got %q", name, value.ValueString())
			}
		}
	})

	t.Run("accepts an empty pool list", func(t *testing.T) {
		got, resp := readPools(t, http.StatusOK, "[]")
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if got.Names.IsUnknown() {
			t.Fatal("an empty answer must not leave names unknown")
		}
		// The data source collects the command lines into a nil slice and the
		// framework renders a nil slice as null rather than as an empty list,
		// so the empty answer arrives as null with no elements. The element
		// count is what the data source guarantees here.
		if elements := got.Names.Elements(); len(elements) != 0 {
			t.Errorf("expected no pool names, got %v", elements)
		}
		if elements := got.Pools.Elements(); len(elements) != 0 {
			t.Errorf("expected no pool entries, got %v", elements)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		_, resp := readPools(t, http.StatusInternalServerError, `{"code":"InternalError","message":"pool listing is unavailable"}`)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error listing pools")
		if !strings.Contains(diagnostic.Detail(), "pool listing is unavailable") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

func poolsFromModel(t *testing.T, model PoolsDataSourceModel) []poolDiagnosticsModel {
	t.Helper()
	var pools []poolDiagnosticsModel
	if model.Pools.IsNull() || model.Pools.IsUnknown() {
		t.Fatalf("pools was not written to the state: %+v", model.Pools)
	}
	if diags := model.Pools.ElementsAs(context.Background(), &pools, false); diags.HasError() {
		t.Fatalf("reading the pool entries: %v", diags)
	}
	return pools
}

// Pool acceptance coverage. The check only proves that the data source reports
// a populated, usable list: the pool count and the volume paths are properties
// of the deployment the tests run against, and the diagnostics are null on
// servers before 1.0.5.

func TestAccPoolsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_pools" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(poolsDataSourceName, "names.#"),
					testAccCheckPoolsNamesNonEmpty(poolsDataSourceName),
					resource.TestCheckResourceAttrSet(poolsDataSourceName, "pools.#"),
					testAccCheckPoolsEntriesMatchNames(poolsDataSourceName),
				),
			},
		},
	})
}

// testAccCheckPoolsNamesNonEmpty requires the pool list to be non-empty and
// every name to be a non-empty string. A running deployment always serves at
// least one pool, so nothing stricter can be asserted here.
func testAccCheckPoolsNamesNonEmpty(dataSourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[dataSourceName]
		if !ok {
			return fmt.Errorf("not found: %s", dataSourceName)
		}

		count, err := strconv.Atoi(rs.Primary.Attributes["names.#"])
		if err != nil {
			return fmt.Errorf("%s: names is not a list: %v", dataSourceName, err)
		}
		if count == 0 {
			return fmt.Errorf("%s: a running deployment must report at least one pool", dataSourceName)
		}

		for i := range count {
			if name := rs.Primary.Attributes[fmt.Sprintf("names.%d", i)]; name == "" {
				return fmt.Errorf("%s: pool %d has an empty name", dataSourceName, i)
			}
		}
		return nil
	}
}

// testAccCheckPoolsEntriesMatchNames requires the nested list to carry one entry
// per name, with the same names and a status. It deliberately does not assert
// the diagnostics: a pre-1.0.5 server reports them as null.
func testAccCheckPoolsEntriesMatchNames(dataSourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[dataSourceName]
		if !ok {
			return fmt.Errorf("not found: %s", dataSourceName)
		}

		names, err := strconv.Atoi(rs.Primary.Attributes["names.#"])
		if err != nil {
			return fmt.Errorf("%s: names is not a list: %v", dataSourceName, err)
		}
		pools, err := strconv.Atoi(rs.Primary.Attributes["pools.#"])
		if err != nil {
			return fmt.Errorf("%s: pools is not a list: %v", dataSourceName, err)
		}
		if names != pools {
			return fmt.Errorf("%s: names has %d entries but pools has %d", dataSourceName, names, pools)
		}

		for i := range pools {
			name := rs.Primary.Attributes[fmt.Sprintf("pools.%d.name", i)]
			if name == "" {
				return fmt.Errorf("%s: pool %d has an empty name", dataSourceName, i)
			}
			if name != rs.Primary.Attributes[fmt.Sprintf("names.%d", i)] {
				return fmt.Errorf("%s: pool %d is %q but names.%d is %q", dataSourceName, i, name, i,
					rs.Primary.Attributes[fmt.Sprintf("names.%d", i)])
			}
			if status := rs.Primary.Attributes[fmt.Sprintf("pools.%d.status", i)]; status == "" {
				return fmt.Errorf("%s: pool %d has an empty status", dataSourceName, i)
			}
		}
		return nil
	}
}
