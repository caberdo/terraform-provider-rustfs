// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// kmsKeysTestPayload follows the RustFS 1.0.x GET /kms/keys shape. The second
// entry omits description, created_by and tags to pin the null handling.
const kmsKeysTestPayload = `{"success":true,"keys":[` +
	`{"key_id":"key-b","description":"archive key","algorithm":"AES_256",` +
	`"usage":"ENCRYPT_DECRYPT","status":"Enabled","tags":{"environment":"test"},` +
	`"created_at":"2026-01-02T00:00:00Z","created_by":"admin"},` +
	`{"key_id":"key-a","algorithm":"AES_256","usage":"ENCRYPT_DECRYPT","status":"Disabled",` +
	`"created_at":"2026-01-01T00:00:00Z"}` +
	`]}`

func newKmsKeysDataSourceModel() *KmsKeysDataSourceModel {
	return &KmsKeysDataSourceModel{
		ID:   types.StringNull(),
		Keys: types.ListNull(kmsKeyListObjectType()),
	}
}

func readKmsKeys(t *testing.T, d *KmsKeysDataSource, sch datasourceschema.Schema) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, newKmsKeysDataSourceModel()),
	}, resp)
	return resp
}

func TestKmsKeysDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewKmsKeysDataSource())

	for _, name := range []string{"id", "keys"} {
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

	keys, ok := sch.Attributes["keys"].(datasourceschema.ListNestedAttribute)
	if !ok || !keys.Computed {
		t.Fatalf("keys must be a computed nested list: %+v", sch.Attributes["keys"])
	}
	want := []string{
		"key_id", "description", "status", "usage", "algorithm", "created_at", "created_by", "tags",
	}
	if len(keys.NestedObject.Attributes) != len(want) {
		t.Errorf("expected %d nested attributes, got %v", len(want), keys.NestedObject.Attributes)
	}
	for _, name := range want {
		attr := keys.NestedObject.Attributes[name]
		if attr == nil || !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
			t.Errorf("keys.%s must be Computed only: %+v", name, attr)
		}
		if attr.GetDescription() == "" {
			t.Errorf("nested attribute %q has no description", name)
		}
	}
	if _, ok := keys.NestedObject.Attributes["tags"].(datasourceschema.MapAttribute); !ok {
		t.Errorf("keys.tags must be a map attribute: %+v", keys.NestedObject.Attributes["tags"])
	}
}

func TestKmsKeysDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewKmsKeysDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_kms_keys" {
		t.Errorf("expected rustfs_kms_keys, got %s", resp.TypeName)
	}
}

func TestKmsKeysDataSourceRead(t *testing.T) {
	client, recorder := routedClient(t,
		requestRoute{method: http.MethodGet, pathContains: "/rustfs/admin/v3/kms/keys", status: http.StatusOK, body: kmsKeysTestPayload},
	)
	d := NewKmsKeysDataSource().(*KmsKeysDataSource)
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	sch := dataSourceSchemaOf(t, d)

	resp := readKmsKeys(t, d, sch)
	requireNoDiagnostics(t, resp.Diagnostics)
	if want := "GET /rustfs/admin/v3/kms/keys"; !recorderSaw(recorder, want) {
		t.Errorf("missing request %q in %v", want, recorder.requests)
	}

	var got KmsKeysDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ID.ValueString() == "" {
		t.Error("id must be set")
	}
	var keys []kmsKeyListModel
	if diags := got.Keys.ElementsAs(context.Background(), &keys, false); diags.HasError() {
		t.Fatalf("reading the keys: %v", diags)
	}
	if len(keys) != 2 {
		t.Fatalf("expected two keys, got %+v", keys)
	}

	first := keys[0]
	if first.KeyID.ValueString() != "key-a" {
		t.Errorf("keys must be sorted by key id, got %+v", keys)
	}
	if first.Status.ValueString() != "Disabled" || first.Usage.ValueString() != "ENCRYPT_DECRYPT" ||
		first.Algorithm.ValueString() != "AES_256" || first.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" {
		t.Errorf("unexpected first key: %+v", first)
	}
	if !first.Description.IsNull() || !first.CreatedBy.IsNull() || !first.Tags.IsNull() {
		t.Errorf("absent fields must be null: %+v", first)
	}

	second := keys[1]
	if second.Description.ValueString() != "archive key" || second.CreatedBy.ValueString() != "admin" {
		t.Errorf("unexpected second key: %+v", second)
	}
	var tags map[string]string
	if diags := second.Tags.ElementsAs(context.Background(), &tags, false); diags.HasError() {
		t.Fatalf("reading the tags: %v", diags)
	}
	if tags["environment"] != "test" {
		t.Errorf("tags = %v", tags)
	}
}

func TestKmsKeysDataSourceReadEmpty(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{method: http.MethodGet, pathContains: "/rustfs/admin/v3/kms/keys", status: http.StatusOK, body: `{"success":true,"keys":[]}`},
	)
	d := NewKmsKeysDataSource().(*KmsKeysDataSource)
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	sch := dataSourceSchemaOf(t, d)

	resp := readKmsKeys(t, d, sch)
	requireNoDiagnostics(t, resp.Diagnostics)

	var got KmsKeysDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if elements := got.Keys.Elements(); len(elements) != 0 {
		t.Errorf("expected no keys, got %v", elements)
	}
}

func TestKmsKeysDataSourceReadError(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{
			method:       http.MethodGet,
			pathContains: "/rustfs/admin/v3/kms/keys",
			status:       http.StatusForbidden,
			body:         s3ErrorXML("AccessDenied", "nope"),
		},
	)
	d := NewKmsKeysDataSource().(*KmsKeysDataSource)
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, &datasource.ConfigureResponse{})
	sch := dataSourceSchemaOf(t, d)

	resp := readKmsKeys(t, d, sch)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error listing KMS keys")
}

// TestAccKmsKeysDataSource creates a real KMS key and reads it back through the
// data source. testAccKmsKeyPreCheck skips when the running backend cannot
// create keys.
func TestAccKmsKeysDataSource(t *testing.T) {
	testAccKmsKeyPreCheck(t)

	name := fmt.Sprintf("tf-kms-key-ds-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckKmsKeyDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccKmsKeyConfig(name, true) + `
data "rustfs_kms_keys" "all" {
  depends_on = [rustfs_kms_key.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_kms_keys.all", "id"),
					testAccCheckKmsKeysContains(
						"data.rustfs_kms_keys.all",
						"rustfs_kms_key.test",
						"key_id",
					),
				),
			},
		},
	})
}

// testAccCheckKmsKeysContains asserts the key the resource created appears in
// the listing under the same key id.
func testAccCheckKmsKeysContains(dataSourceName, resourceName, attribute string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		keyResource, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("not found: %s", resourceName)
		}
		wantKeyID := keyResource.Primary.Attributes[attribute]
		if wantKeyID == "" {
			return fmt.Errorf("%s has no %s in state", resourceName, attribute)
		}

		dataSource, ok := s.RootModule().Resources[dataSourceName]
		if !ok {
			return fmt.Errorf("not found: %s", dataSourceName)
		}
		attrs := dataSource.Primary.Attributes
		count := 0
		if _, err := fmt.Sscanf(attrs["keys.#"], "%d", &count); err != nil {
			return fmt.Errorf("reading keys.# of %s: %w", dataSourceName, err)
		}
		for i := range count {
			if attrs[fmt.Sprintf("keys.%d.%s", i, attribute)] == wantKeyID {
				return nil
			}
		}
		return fmt.Errorf("key %s is missing from the %s listing", wantKeyID, dataSourceName)
	}
}
