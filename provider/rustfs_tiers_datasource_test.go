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

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// tiersTestPayload follows the RustFS 1.0.x GET /tier shape: each backend is
// nested under its type and carries its credentials.
const tiersTestPayload = `[` +
	`{"type":"rustfs","rustfs":{"name":"COLD","endpoint":"https://cold.example.org",` +
	`"access_key":"AK","secret_key":"original-secret","bucket":"cold"}},` +
	`{"type":"s3","s3":{"name":"ARCHIVE","endpoint":"https://archive.example.org",` +
	`"accessKey":"AK2","secretKey":"camel-secret"}}` +
	`]`

func newTiersDataSourceModel() *TiersDataSourceModel {
	return &TiersDataSourceModel{
		ID:    types.StringNull(),
		Tiers: types.ListNull(tierListObjectType()),
	}
}

func readTiers(t *testing.T, d *TiersDataSource, sch datasourceschema.Schema) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, newTiersDataSourceModel()),
	}, resp)
	return resp
}

func TestTiersDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewTiersDataSource())

	for _, name := range []string{"id", "tiers"} {
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

	tiers, ok := sch.Attributes["tiers"].(datasourceschema.ListNestedAttribute)
	if !ok || !tiers.Computed {
		t.Fatalf("tiers must be a computed nested list: %+v", sch.Attributes["tiers"])
	}
	if _, ok := tiers.NestedObject.Attributes["config_json"].(datasourceschema.StringAttribute); !ok {
		t.Errorf("tiers.config_json must be a string attribute: %+v", tiers.NestedObject.Attributes)
	}
	if len(tiers.NestedObject.Attributes) != 3 {
		t.Errorf("expected name, tier_type and config_json, got %v", tiers.NestedObject.Attributes)
	}
	for name, attr := range tiers.NestedObject.Attributes {
		if !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
			t.Errorf("tiers.%s must be Computed only", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("nested attribute %q has no description", name)
		}
	}
}

func TestTiersDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewTiersDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_tiers" {
		t.Errorf("expected rustfs_tiers, got %s", resp.TypeName)
	}
}

func TestTiersDataSourceRead(t *testing.T) {
	client, recorder := routedClient(t,
		requestRoute{method: http.MethodGet, pathContains: "/rustfs/admin/v3/tier", status: http.StatusOK, body: tiersTestPayload},
	)
	d := NewTiersDataSource().(*TiersDataSource)
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	sch := dataSourceSchemaOf(t, d)

	resp := readTiers(t, d, sch)
	requireNoDiagnostics(t, resp.Diagnostics)
	if want := "GET /rustfs/admin/v3/tier"; !recorderSaw(recorder, want) {
		t.Errorf("missing request %q in %v", want, recorder.requests)
	}

	var got TiersDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ID.ValueString() == "" {
		t.Error("id must be set")
	}
	var tiers []tierListModel
	if diags := got.Tiers.ElementsAs(context.Background(), &tiers, false); diags.HasError() {
		t.Fatalf("reading the tiers: %v", diags)
	}
	if len(tiers) != 2 {
		t.Fatalf("expected two tiers, got %+v", tiers)
	}
	if tiers[0].Name.ValueString() != "ARCHIVE" || tiers[1].Name.ValueString() != "COLD" {
		t.Errorf("tiers must be sorted by name, got %+v", tiers)
	}
	if tiers[1].TierType.ValueString() != "rustfs" {
		t.Errorf("tier_type = %q", tiers[1].TierType.ValueString())
	}

	cold := tiers[1].ConfigJSON.ValueString()
	if !strings.Contains(cold, `"secret_key":"REDACTED"`) {
		t.Errorf("secret_key must be redacted, got %s", cold)
	}
	if strings.Contains(cold, "original-secret") {
		t.Errorf("the original secret leaked into config_json: %s", cold)
	}
	if !strings.Contains(cold, `"access_key":"AK"`) || !strings.Contains(cold, `"bucket":"cold"`) {
		t.Errorf("non-secret backend fields must survive: %s", cold)
	}
	archive := tiers[0].ConfigJSON.ValueString()
	if !strings.Contains(archive, `"secretKey":"REDACTED"`) {
		t.Errorf("camelCase secretKey must be redacted, got %s", archive)
	}
	if strings.Contains(archive, "camel-secret") {
		t.Errorf("the original camelCase secret leaked into config_json: %s", archive)
	}
}

func TestTiersDataSourceReadEmpty(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{method: http.MethodGet, pathContains: "/rustfs/admin/v3/tier", status: http.StatusOK, body: `[]`},
	)
	d := NewTiersDataSource().(*TiersDataSource)
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	sch := dataSourceSchemaOf(t, d)

	resp := readTiers(t, d, sch)
	requireNoDiagnostics(t, resp.Diagnostics)

	var got TiersDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if elements := got.Tiers.Elements(); len(elements) != 0 {
		t.Errorf("expected no tiers, got %v", elements)
	}
}

func TestTiersDataSourceReadError(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{
			method:       http.MethodGet,
			pathContains: "/rustfs/admin/v3/tier",
			status:       http.StatusForbidden,
			body:         s3ErrorXML("AccessDenied", "nope"),
		},
	)
	d := NewTiersDataSource().(*TiersDataSource)
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	sch := dataSourceSchemaOf(t, d)

	resp := readTiers(t, d, sch)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error listing tiers")
}

// TestAccTiersDataSource creates a real tier and reads it back through the
// data source. A tier needs a second endpoint the server can probe, so the test
// skips when RUSTFS_TIER_ENDPOINT is not set.
func TestAccTiersDataSource(t *testing.T) {
	tierTestPreCheck(t)

	tierEndpoint := tierTestEndpoint(t)
	s3Endpoint := tierTestS3Endpoint(t)
	name := fmt.Sprintf("TFACCTIERDS%d", acctest.RandInt())
	bucket := fmt.Sprintf("tf-acc-tier-ds-%d", acctest.RandInt())

	backendBucket := tierTestBucket{endpoint: s3Endpoint, name: bucket}
	backendBucket.create(t)
	defer backendBucket.delete(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTierDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccTierConfig(tierEndpoint, name, bucket) + `
data "rustfs_tiers" "all" {
  depends_on = [rustfs_tier.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_tiers.all", "id"),
					testAccCheckTiersContains("data.rustfs_tiers.all", name, "rustfs", os.Getenv("RUSTFS_SECRET")),
				),
			},
		},
	})
}

// testAccCheckTiersContains finds the tier by name in the listing and checks
// that the secret it was created with never appears in the redacted config.
func testAccCheckTiersContains(dataSourceName, tierName, tierType, forbiddenSecret string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[dataSourceName]
		if !ok {
			return fmt.Errorf("not found: %s", dataSourceName)
		}
		attrs := rs.Primary.Attributes
		count := 0
		if _, err := fmt.Sscanf(attrs["tiers.#"], "%d", &count); err != nil {
			return fmt.Errorf("reading tiers.# of %s: %w", dataSourceName, err)
		}
		for i := range count {
			prefix := fmt.Sprintf("tiers.%d.", i)
			if attrs[prefix+"name"] != tierName {
				continue
			}
			if got := attrs[prefix+"tier_type"]; got != tierType {
				return fmt.Errorf("tier %s has tier_type %q, want %q", tierName, got, tierType)
			}
			config := attrs[prefix+"config_json"]
			if config == "" {
				return fmt.Errorf("tier %s has no config_json", tierName)
			}
			if forbiddenSecret != "" {
				for _, field := range []string{"secret_key", "secretKey"} {
					if strings.Contains(config, `"`+field+`":"`+forbiddenSecret+`"`) {
						return fmt.Errorf("tier %s leaked its secret in %s: %s", tierName, field, config)
					}
				}
			}
			if !strings.Contains(config, "REDACTED") {
				return fmt.Errorf("tier %s does not report a redacted credential: %s", tierName, config)
			}
			return nil
		}
		return fmt.Errorf("tier %s is missing from the %s listing", tierName, dataSourceName)
	}
}
