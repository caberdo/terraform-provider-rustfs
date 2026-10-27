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

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func configHistoryClearModel(confirm string) *ConfigHistoryClearResourceModel {
	return &ConfigHistoryClearResourceModel{
		ID:        types.StringNull(),
		Confirm:   types.StringValue(confirm),
		Triggers:  nullStringMap(),
		ClearedAt: types.StringNull(),
	}
}

func TestConfigHistoryClearResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewConfigHistoryClearResource())

	attrs := sch.GetAttributes()
	if len(attrs) != 4 {
		t.Errorf("expected 4 attributes, got %d: %v", len(attrs), attrs)
	}

	id, ok := attrs["id"].(schema.StringAttribute)
	if !ok || !id.Computed || id.Required || id.Optional {
		t.Errorf("id must be Computed only: %+v", attrs["id"])
	}

	confirm, ok := attrs["confirm"].(schema.StringAttribute)
	if !ok || !confirm.Required || confirm.Optional || confirm.Computed {
		t.Errorf("confirm must be Required only: %+v", attrs["confirm"])
	}

	triggers, ok := attrs["triggers"].(schema.MapAttribute)
	if !ok || !triggers.Optional || triggers.Required || triggers.Computed {
		t.Errorf("triggers must be Optional only: %+v", attrs["triggers"])
	}

	clearedAt, ok := attrs["cleared_at"].(schema.StringAttribute)
	if !ok || !clearedAt.Computed || clearedAt.Required || clearedAt.Optional {
		t.Errorf("cleared_at must be Computed only: %+v", attrs["cleared_at"])
	}

	for _, name := range []string{"entries_cleared", "dry_run"} {
		if _, exists := attrs[name]; exists {
			t.Errorf("%s must not exist: the server reports no count and supports no dry run", name)
		}
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestConfigHistoryClearResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewConfigHistoryClearResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_config_history_clear" {
		t.Errorf("expected rustfs_config_history_clear, got %s", resp.TypeName)
	}
}

func TestConfigHistoryClearResourceValidateConfig(t *testing.T) {
	t.Run("rejects a missing confirmation", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), cannedClient(t, http.StatusOK, ""))
		plan := configHistoryClearModel("")
		plan.Confirm = types.StringNull()

		resp := h.validateConfig(plan)

		assertDiagnosticSummary(t, resp.Diagnostics, "Missing config history clear confirmation")
	})

	t.Run("rejects a mismatched confirmation", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.validateConfig(configHistoryClearModel("clear"))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Invalid config history clear confirmation")
		if !strings.Contains(diagnostic.Detail(), "clear-config-history") {
			t.Errorf("detail = %q", diagnostic.Detail())
		}
	})

	t.Run("defers an unknown confirmation to apply", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), cannedClient(t, http.StatusOK, ""))
		plan := configHistoryClearModel("")
		plan.Confirm = types.StringUnknown()

		resp := h.validateConfig(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
	})

	t.Run("accepts the exact literal", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.validateConfig(configHistoryClearModel("clear-config-history"))

		requireNoDiagnostics(t, resp.Diagnostics)
	})
}

func TestConfigHistoryClearResourceCreate(t *testing.T) {
	t.Run("clears the history and records the result", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), newUnitAllClient(srv))

		resp := h.create(configHistoryClearModel("clear-config-history"))
		requireNoDiagnostics(t, resp.Diagnostics)

		request := recorder.only(t)
		if request.Method != http.MethodDelete || request.Path != "/rustfs/admin/v3/clear-config-history-kv" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if request.Query != "restoreId=all" {
			t.Errorf("query = %q, want restoreId=all", request.Query)
		}

		var got ConfigHistoryClearResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ID.ValueString() != "config-history-clear" {
			t.Errorf("id = %q", got.ID.ValueString())
		}
		clearedAt, err := time.Parse(time.RFC3339, got.ClearedAt.ValueString())
		if err != nil {
			t.Fatalf("cleared_at %q is not RFC 3339: %v", got.ClearedAt.ValueString(), err)
		}
		if _, offset := clearedAt.Zone(); offset != 0 {
			t.Errorf("cleared_at %q is not UTC", got.ClearedAt.ValueString())
		}
	})

	t.Run("refuses a mismatched confirmation without calling the server", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), client)
		plan := configHistoryClearModel("nope")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid config history clear confirmation", "clear-config-history")
		rec.assertNoRequest(t, "clear-config-history-kv")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports an incomplete confirmation", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), client)
		plan := configHistoryClearModel("")
		plan.Confirm = types.StringUnknown()

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Incomplete config history clear", "confirm must be resolved")
		rec.assertNoRequest(t, "clear-config-history-kv")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports a server failure and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method:       http.MethodDelete,
			pathContains: "clear-config-history-kv",
			status:       http.StatusInternalServerError,
			body:         s3ErrorXML("InternalError", "clear history failed"),
		})
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), client)
		plan := configHistoryClearModel("clear-config-history")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error clearing config history", "clear history failed")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports a capability gap with the server version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodDelete, pathContains: "clear-config-history-kv", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), client)
		plan := configHistoryClearModel("clear-config-history")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error clearing config history", "RustFS 1.0.3")
		assertDiagnosticDetail(t, resp.Diagnostics, "DELETE /rustfs/admin/v3/clear-config-history-kv")
		h.assertNoPartialState(resp, plan)
	})
}

func TestConfigHistoryClearResourceUpdate(t *testing.T) {
	t.Run("re-runs the clear and refreshes cleared_at when a trigger changes", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodDelete,
			pathContains: "clear-config-history-kv",
			status:       http.StatusOK,
			body:         "",
		})
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), client)

		state := configHistoryClearModel("clear-config-history")
		state.ID = types.StringValue("config-history-clear")
		state.ClearedAt = types.StringValue("2026-10-20T09:15:00Z")

		plan := configHistoryClearModel("clear-config-history")
		plan.Triggers = triggersMap(t, "run", "2")

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "clear-config-history-kv")
		assertStateString(t, resp.State, path.Root("id"), "config-history-clear")
		var got ConfigHistoryClearResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ClearedAt.ValueString() == "2026-10-20T09:15:00Z" || got.ClearedAt.ValueString() == "" {
			t.Errorf("cleared_at was not refreshed: %q", got.ClearedAt.ValueString())
		}
	})

	t.Run("reports a failed re-run without moving state", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodDelete,
			pathContains: "clear-config-history-kv",
			status:       http.StatusConflict,
			body:         s3ErrorXML("OperationAborted", "config mutation is busy"),
		})
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), client)

		state := configHistoryClearModel("clear-config-history")
		state.ID = types.StringValue("config-history-clear")
		state.ClearedAt = types.StringValue("2026-10-20T09:15:00Z")

		plan := configHistoryClearModel("clear-config-history")
		plan.Triggers = triggersMap(t, "run", "3")

		resp := h.update(plan, state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error clearing config history", "config mutation is busy")
		assertStateString(t, resp.State, path.Root("cleared_at"), "2026-10-20T09:15:00Z")
		rec.assertRequest(t, "clear-config-history-kv")
	})
}

func TestConfigHistoryClearResourceRead(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewConfigHistoryClearResource(), client)

	state := configHistoryClearModel("clear-config-history")
	state.ID = types.StringValue("config-history-clear")
	state.ClearedAt = types.StringValue("2026-10-20T09:15:00Z")

	resp := h.read(state)

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)
	assertStateString(t, resp.State, path.Root("id"), "config-history-clear")
	assertStateString(t, resp.State, path.Root("cleared_at"), "2026-10-20T09:15:00Z")
	rec.assertNoRequest(t, "clear-config-history-kv")
}

func TestConfigHistoryClearResourceDelete(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewConfigHistoryClearResource(), client)

	state := configHistoryClearModel("clear-config-history")
	state.ID = types.StringValue("config-history-clear")
	state.ClearedAt = types.StringValue("2026-10-20T09:15:00Z")

	resp := h.delete(state)

	requireNoDiagnostics(t, resp.Diagnostics)
	rec.assertNoRequest(t, "clear-config-history-kv")
}

func TestConfigHistoryClearResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure(nil)

		requireNoDiagnostics(t, resp.Diagnostics)
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})

	t.Run("all client", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigHistoryClearResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure(cannedClient(t, http.StatusOK, ""))

		requireNoDiagnostics(t, resp.Diagnostics)
	})
}

func TestConfigHistoryClearResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	clearedState := func() *ConfigHistoryClearResourceModel {
		state := configHistoryClearModel("clear-config-history")
		state.ID = types.StringValue("config-history-clear")
		state.ClearedAt = types.StringValue("2026-10-20T09:15:00Z")
		return state
	}

	cases := []struct {
		name   string
		routes []requestRoute
		run    func(t *testing.T, h *crudHarness, rec *recordingServer)
	}{
		{
			name: "create: accepted clear records the result",
			routes: []requestRoute{{
				method:       http.MethodDelete,
				pathContains: "clear-config-history-kv",
				status:       http.StatusOK,
				body:         "",
			}},
			run: func(t *testing.T, h *crudHarness, rec *recordingServer) {
				resp := h.create(configHistoryClearModel("clear-config-history"))

				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateString(t, resp.State, path.Root("id"), "config-history-clear")
				assertStatePresent(t, resp.State)
				rec.assertRequest(t, "DELETE /rustfs/admin/v3/clear-config-history-kv")
			},
		},
		{
			name: "create: a failed clear writes no partial state",
			routes: []requestRoute{{
				method:       http.MethodDelete,
				pathContains: "clear-config-history-kv",
				status:       http.StatusInternalServerError,
				body:         s3ErrorXML("InternalError", "clear failed"),
			}},
			run: func(t *testing.T, h *crudHarness, rec *recordingServer) {
				plan := configHistoryClearModel("clear-config-history")

				resp := h.create(plan)

				assertErrorDiagnostic(t, resp.Diagnostics, "Error clearing config history", "clear failed")
				h.assertNoPartialState(resp, plan)
				rec.assertRequest(t, "clear-config-history-kv")
			},
		},
		{
			name: "read: a refresh echoes the record without calling the server",
			run: func(t *testing.T, h *crudHarness, rec *recordingServer) {
				resp := h.read(clearedState())

				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("cleared_at"), "2026-10-20T09:15:00Z")
				rec.assertNoRequest(t, "clear-config-history-kv")
			},
		},
		{
			name: "destroy: never clears the history again",
			run: func(t *testing.T, h *crudHarness, rec *recordingServer) {
				resp := h.delete(clearedState())

				requireNoDiagnostics(t, resp.Diagnostics)
				rec.assertNoRequest(t, "clear-config-history-kv")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewConfigHistoryClearResource(), client)

			tc.run(t, h, rec)
		})
	}
}

func TestAccConfigHistoryClearResource(t *testing.T) {
	scope := fmt.Sprintf("notify_webhook:tfaccclear%d", acctest.RandInt())
	var preClearRestoreID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccConfigHistoryClearPreCheck(t) },
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

data "rustfs_config_history" "before" {
  max_entries = 10
  depends_on  = [rustfs_config.test]
}
`, scope),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.rustfs_config_history.before", "entries.0.restore_id"),
					resource.TestCheckResourceAttrWith("data.rustfs_config_history.before", "entries.0.restore_id",
						func(value string) error {
							preClearRestoreID = value
							return nil
						}),
				),
			},
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_config" "test" {
  sub_system = %q
  settings = {
    endpoint = "http://127.0.0.1:18080/rustfs/events"
  }
}

resource "rustfs_config_history_clear" "test" {
  confirm = "clear-config-history"

  triggers = {
    run = "acceptance"
  }
}

data "rustfs_config_history" "after" {
  max_entries = 10
  depends_on  = [rustfs_config_history_clear.test]
}
`, scope),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rustfs_config_history_clear.test", "id", "config-history-clear"),
					resource.TestCheckResourceAttrSet("rustfs_config_history_clear.test", "cleared_at"),
					resource.TestCheckResourceAttr("data.rustfs_config_history.after", "entries.#", "0"),
					testAccRestoreDeletedHistoryFails(&preClearRestoreID),
				),
			},
		},
	})
}

// testAccRestoreDeletedHistoryFails proves the clear removed the rollback
// point: restoring the id captured before the clear must now fail on the
// server. The id only exists after the first step runs, which is why this is a
// check instead of a later TestStep config.
func testAccRestoreDeletedHistoryFails(restoreID *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if *restoreID == "" {
			return fmt.Errorf("the pre-clear restore id was not captured")
		}
		client := testAccRustClient()
		err := client.RestoreConfigHistory(*restoreID)
		if err == nil {
			return fmt.Errorf("restoring the cleared entry %q succeeded, want a not-found error", *restoreID)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "not found") &&
			!strings.Contains(err.Error(), "NoSuchKey") {
			return fmt.Errorf("restoring the cleared entry %q failed with an unexpected error: %w", *restoreID, err)
		}
		return nil
	}
}

// testAccConfigHistoryClearPreCheck proves the running server implements the
// clear route before the test applies it, and skips cleanly when it does not.
// The probe clear is harmless: the test's first step creates a new history
// entry, which the test then deletes.
func testAccConfigHistoryClearPreCheck(t *testing.T) {
	testAccPreCheck(t)

	client := testAccRustClient()
	if err := client.ClearConfigHistory(); err != nil {
		if rustfs.IsUnsupportedFeature(err) {
			t.Skipf("config history clear is not implemented by the running RustFS, skipping: %v", err)
		}
		t.Fatalf("clearing config history: %v", err)
	}
}
