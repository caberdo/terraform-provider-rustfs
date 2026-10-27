// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const auditTargetResourceName = "rustfs_audit_target.test"

func TestAuditTargetSchema(t *testing.T) {
	r := NewAuditTargetResource()
	resp := &frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), frameworkresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()

	// The legacy flat attributes must keep working verbatim.
	for _, name := range []string{
		"target_type", "target_name", "endpoint", "auth_token", "comment",
		"queue_limit", "queue_dir", "client_cert", "client_key", "client_ca",
		"skip_tls_verify", "health_state", "health_reason", "status",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing legacy attribute %s", name)
		}
	}

	// enable is Optional+Computed with a true default.
	enable, ok := attrs["enable"].(schema.BoolAttribute)
	if !ok {
		t.Fatalf("expected enable to be a BoolAttribute, got %T", attrs["enable"])
	}
	if !enable.Optional {
		t.Error("enable should be Optional")
	}
	if !enable.Computed {
		t.Error("enable should be Computed")
	}
	if enable.Default == nil {
		t.Fatal("enable should have a default")
	}
	enableDefault := &defaults.BoolResponse{}
	enable.Default.DefaultBool(context.Background(), defaults.BoolRequest{}, enableDefault)
	if !enableDefault.PlanValue.ValueBool() {
		t.Error("enable default should be true")
	}

	// The nine per-type configuration blocks must all be present.
	for _, name := range []string{
		"webhook_config", "amqp_config", "kafka_config", "mqtt_config", "nats_config",
		"postgres_config", "mysql_config", "redis_config", "pulsar_config",
	} {
		attr, ok := attrs[name]
		if !ok {
			t.Errorf("missing %s attribute", name)
			continue
		}
		if _, ok := attr.(schema.SingleNestedAttribute); !ok {
			t.Errorf("%s should be a SingleNestedAttribute, got %T", name, attr)
		}
	}
}

func TestAuditTargetKeyValuesLegacy(t *testing.T) {
	r := &auditTargetResource{}
	model := auditTargetResourceModel{
		TargetType:    types.StringValue("audit_webhook"),
		TargetName:    types.StringValue("legacy"),
		Enable:        types.BoolValue(true),
		Endpoint:      types.StringValue("https://hooks.example.com/webhook/x"),
		AuthToken:     types.StringValue("superSecret"),
		Comment:       types.StringValue("managed by terraform"),
		QueueDir:      types.StringValue("/var/lib/rustfs/queue"),
		ClientCert:    types.StringValue("client-cert"),
		ClientKey:     types.StringValue("client-key"),
		ClientCA:      types.StringValue("client-ca"),
		QueueLimit:    types.Int64Value(100),
		SkipTLSVerify: types.BoolValue(true),
	}

	kvs, err := r.auditTargetKeyValues(&model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Regression pin: the legacy flat path must keep its exact ordering,
	// including the trailing enable pair the server already defaulted to on.
	want := []rustfs.TargetKeyValue{
		{Key: "endpoint", Value: "https://hooks.example.com/webhook/x"},
		{Key: "auth_token", Value: "superSecret"},
		{Key: "comment", Value: "managed by terraform"},
		{Key: "queue_dir", Value: "/var/lib/rustfs/queue"},
		{Key: "client_cert", Value: "client-cert"},
		{Key: "client_key", Value: "client-key"},
		{Key: "client_ca", Value: "client-ca"},
		{Key: "queue_limit", Value: "100"},
		{Key: "skip_tls_verify", Value: "true"},
		{Key: "enable", Value: "on"},
	}

	if len(kvs) != len(want) {
		t.Fatalf("expected %d key/value pairs, got %d: %+v", len(want), len(kvs), kvs)
	}
	for i := range want {
		if kvs[i] != want[i] {
			t.Errorf("pair %d: got %+v, want %+v", i, kvs[i], want[i])
		}
	}
}

func TestAuditTargetKeyValuesTyped(t *testing.T) {
	r := &auditTargetResource{}
	model := auditTargetResourceModel{
		TargetType: types.StringValue("audit_redis"),
		TargetName: types.StringValue("typed"),
		Enable:     types.BoolValue(true),
		Comment:    types.StringValue("typed comment"),
		Redis: &targetRedisConfigModel{
			URL:     types.StringValue("redis://127.0.0.1:6379/"),
			Channel: types.StringValue("tf-audit"),
		},
	}

	kvs, err := r.auditTargetKeyValues(&model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []rustfs.TargetKeyValue{
		{Key: "url", Value: "redis://127.0.0.1:6379/"},
		{Key: "channel", Value: "tf-audit"},
		{Key: "comment", Value: "typed comment"},
		{Key: "enable", Value: "on"},
	}

	if len(kvs) != len(want) {
		t.Fatalf("expected %d key/value pairs, got %d: %+v", len(want), len(kvs), kvs)
	}
	for i := range want {
		if kvs[i] != want[i] {
			t.Errorf("pair %d: got %+v, want %+v", i, kvs[i], want[i])
		}
	}
}

func TestAuditTargetValidate(t *testing.T) {
	tests := []struct {
		name    string
		model   auditTargetResourceModel
		wantErr string
	}{
		{
			name: "block for wrong type",
			model: auditTargetResourceModel{
				TargetType: types.StringValue("audit_amqp"),
				Redis: &targetRedisConfigModel{
					URL: types.StringValue("redis://127.0.0.1:6379/"),
				},
			},
			wantErr: `target type "audit_amqp" requires the amqp_config block`,
		},
		{
			name: "no block with non-webhook type",
			model: auditTargetResourceModel{
				TargetType: types.StringValue("audit_amqp"),
			},
			wantErr: `target type "audit_amqp" requires the amqp_config block`,
		},
		{
			name: "no block with webhook type is ok",
			model: auditTargetResourceModel{
				TargetType: types.StringValue("audit_webhook"),
				Endpoint:   types.StringValue("https://hooks.example.com/webhook/x"),
			},
		},
		{
			name: "omitted target_type with flat webhook attributes is ok",
			model: auditTargetResourceModel{
				TargetType: types.StringNull(),
				Endpoint:   types.StringValue("https://hooks.example.com/webhook/x"),
			},
		},
		{
			name: "omitted target_type with non-webhook block errors",
			model: auditTargetResourceModel{
				TargetType: types.StringNull(),
				Redis: &targetRedisConfigModel{
					URL: types.StringValue("redis://127.0.0.1:6379/"),
				},
			},
			wantErr: `target type "audit_webhook" requires the webhook_config block`,
		},
		{
			name: "unknown target_type with matching block is ok",
			model: auditTargetResourceModel{
				TargetType: types.StringUnknown(),
				Redis: &targetRedisConfigModel{
					URL: types.StringValue("redis://127.0.0.1:6379/"),
				},
			},
		},
		{
			name: "flat webhook attributes combined with block",
			model: auditTargetResourceModel{
				TargetType: types.StringValue("audit_webhook"),
				Endpoint:   types.StringValue("https://hooks.example.com/webhook/x"),
				Webhook: &targetWebhookConfigModel{
					Endpoint: types.StringValue("https://hooks.example.com/webhook/x"),
				},
			},
			wantErr: "the flat webhook attributes cannot be combined with a configuration block",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.model.validateConfig()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Errorf("expected error %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestAccAuditTargetResource(t *testing.T) {
	name := fmt.Sprintf("tf-audit-%d", acctest.RandInt())
	replacementName := fmt.Sprintf("tf-audit-%d", acctest.RandInt())
	endpoint := fmt.Sprintf("https://hooks.example.com/webhook/%s", name)
	updatedEndpoint := endpoint + "-updated"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccAuditTargetPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAuditTargetDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccAuditTargetConfig(name, endpoint, "managed by terraform"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckAuditTargetExists(name),
					resource.TestCheckResourceAttr(auditTargetResourceName, "target_type", "audit_webhook"),
					resource.TestCheckResourceAttr(auditTargetResourceName, "target_name", name),
					resource.TestCheckResourceAttr(auditTargetResourceName, "endpoint", endpoint),
					resource.TestCheckResourceAttr(auditTargetResourceName, "auth_token", "superSecret"),
					resource.TestCheckResourceAttr(auditTargetResourceName, "comment", "managed by terraform"),
					resource.TestCheckResourceAttrSet(auditTargetResourceName, "health_state"),
					// The list endpoint does not echo the target configuration,
					// so the create is confirmed against get-config-kv, which
					// renders what the server actually stored.
					testAccCheckAuditTargetConfigValue(name, "endpoint", endpoint),
				),
			},
			{
				// In-place change: endpoint and comment are mutable, so the plan
				// is an update (never a replacement) and the server has to hand
				// the new endpoint back.
				Config: testAccAuditTargetConfig(name, updatedEndpoint, "updated by terraform"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(auditTargetResourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckAuditTargetExists(name),
					resource.TestCheckResourceAttr(auditTargetResourceName, "comment", "updated by terraform"),
					resource.TestCheckResourceAttr(auditTargetResourceName, "endpoint", updatedEndpoint),
					resource.TestCheckResourceAttrSet(auditTargetResourceName, "health_state"),
					testAccCheckAuditTargetConfigValue(name, "endpoint", updatedEndpoint),
				),
			},
			{
				// Revert the update; the server has to end up on the original
				// endpoint again, which the config read-back proves.
				Config: testAccAuditTargetConfig(name, endpoint, "managed by terraform"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(auditTargetResourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckAuditTargetExists(name),
					resource.TestCheckResourceAttr(auditTargetResourceName, "comment", "managed by terraform"),
					resource.TestCheckResourceAttr(auditTargetResourceName, "endpoint", endpoint),
					testAccCheckAuditTargetConfigValue(name, "endpoint", endpoint),
				),
			},
			{
				// Explicit empty second plan: the state the revert wrote is the
				// configuration, so the plan made from that state proposes no
				// change. This is what catches a computed attribute (health
				// state, status) that the read path drops.
				Config:             testAccAuditTargetConfig(name, endpoint, "managed by terraform"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// target_name carries RequiresReplace, so renaming the target
				// has to be planned as a replacement. The step only plans: the
				// replacement is asserted here and applied in the next step.
				Config:             testAccAuditTargetConfig(replacementName, endpoint, "managed by terraform"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(auditTargetResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement so the destroy-and-recreate path really
				// runs, and check the old target is gone while the new one is on
				// the server with the configuration intact.
				Config: testAccAuditTargetConfig(replacementName, endpoint, "managed by terraform"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(auditTargetResourceName, plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckAuditTargetExists(replacementName),
					testAccCheckAuditTargetGone(name),
					resource.TestCheckResourceAttr(auditTargetResourceName, "target_name", replacementName),
					resource.TestCheckResourceAttr(auditTargetResourceName, "endpoint", endpoint),
					testAccCheckAuditTargetConfigValue(replacementName, "endpoint", endpoint),
				),
			},
			{
				// Out-of-band drift: the Check function resets the target
				// straight through the admin API after the (no-op) apply, so the
				// plan made before the refresh is still empty while the plan
				// Terraform proposes after the refresh has to re-create it.
				Config:             testAccAuditTargetConfig(replacementName, endpoint, "managed by terraform"),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(auditTargetResourceName, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckAuditTargetExists(replacementName),
					testAccAuditTargetResetOutOfBand(replacementName),
				),
			},
			{
				// Apply the drift the previous step discovered and assert the
				// re-created target is on the server again with its config.
				Config: testAccAuditTargetConfig(replacementName, endpoint, "managed by terraform"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckAuditTargetExists(replacementName),
					resource.TestCheckResourceAttr(auditTargetResourceName, "target_name", replacementName),
					testAccCheckAuditTargetConfigValue(replacementName, "endpoint", endpoint),
				),
			},
			{
				ResourceName:      auditTargetResourceName,
				ImportState:       true,
				ImportStateId:     "audit_webhook/" + replacementName,
				ImportStateVerify: true,
				// The admin list endpoint does not echo config values, so these
				// attributes cannot be verified after import.
				ImportStateVerifyIdentifierAttribute: "target_name",
				ImportStateVerifyIgnore:              []string{"enable", "endpoint", "auth_token", "comment", "queue_limit", "queue_dir", "client_cert", "client_key", "client_ca", "skip_tls_verify", "health_state", "health_reason", "status"},
			},
			{
				// Import accepts the composite <target_type>/<target_name> ID
				// only. An ID without the separator has to produce the explicit
				// diagnostic rather than a panic or a half-filled state.
				ResourceName:  auditTargetResourceName,
				ImportState:   true,
				ImportStateId: "no-separator-here",
				ExpectError:   regexp.MustCompile(`(?s)Invalid import ID.*Expected the import ID to be <target_type>/<target_name>`),
			},
		},
	})
}

// testAccAuditTargetPreCheck verifies the environment and that the running
// RustFS has the audit module enabled (the admin API rejects target management
// otherwise). The list endpoint succeeds even when the module is disabled, so
// the check enables the module when it is console/default sourced and probes an
// actual create; when the module is pinned by an environment variable the test
// skips (testAccEnsureModuleEnabled).
func testAccAuditTargetPreCheck(t *testing.T) {
	testAccPreCheck(t)
	testAccEnsureModuleEnabled(t, "audit")

	client := testAccRustClient()
	probe := fmt.Sprintf("tf-audit-probe-%d", acctest.RandInt())
	probeConfig := []rustfs.TargetKeyValue{
		{Key: "endpoint", Value: "https://hooks.example.com/webhook/" + probe},
	}
	err := client.SetAuditTarget(context.Background(), "audit_webhook", probe, probeConfig)
	if err != nil {
		if strings.Contains(err.Error(), "audit module is disabled") {
			t.Skipf("audit module is disabled on the running RustFS; skipping: %v", err)
		}
		t.Fatalf("audit target management is not available on the running RustFS, skipping: %v", err)
	}
	// Clean up the probe target so it does not linger in the server config.
	if err := client.ResetAuditTarget(context.Background(), "audit_webhook", probe); err != nil {
		t.Logf("could not clean up audit target probe %s: %v", probe, err)
	}
}

func testAccAuditTargetConfig(name, endpoint, comment string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_audit_target" "test" {
  target_type = "audit_webhook"
  target_name = "%s"
  endpoint    = "%s"
  auth_token  = "superSecret"
  comment     = "%s"
}
`, name, endpoint, comment)
}

func testAccCheckAuditTargetExists(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[auditTargetResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", auditTargetResourceName)
		}
		targetName := rs.Primary.Attributes["target_name"]
		if targetName == "" {
			return fmt.Errorf("no target_name set")
		}

		client := testAccRustClient()
		targets, err := client.ListAuditTargets(context.Background())
		if err != nil {
			return err
		}
		for _, target := range targets {
			if target.AccountID == name && target.Service == "webhook" {
				return nil
			}
		}
		return fmt.Errorf("audit target %s not found in list", name)
	}
}

// testAccCheckAuditTargetConfigValue asserts a configuration key of the target
// as the server renders it back through get-config-kv
// ("audit_webhook:<name>"). The admin list endpoint only reports identity and
// health, so this is the only server-side read-back of what an update wrote.
func testAccCheckAuditTargetConfigValue(name, key, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig(context.Background(), "audit_webhook:"+name)
		if err != nil {
			return fmt.Errorf("failed to read audit target %s config from the server: %w", name, err)
		}
		for _, kv := range kvs {
			if kv.Key == key {
				if kv.Value != want {
					return fmt.Errorf("server reports audit target %s %s = %q, want %q", name, key, kv.Value, want)
				}
				return nil
			}
		}
		return fmt.Errorf("server does not report key %q for audit target %s: %+v", key, name, kvs)
	}
}

// testAccCheckAuditTargetGone asserts the named audit target is not configured
// on the server any more, which is how a replacement proves the old instance
// was destroyed.
func testAccCheckAuditTargetGone(name string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		targets, err := client.ListAuditTargets(context.Background())
		if err != nil {
			return err
		}
		for _, target := range targets {
			if strings.EqualFold(target.AccountID, name) {
				return fmt.Errorf("audit target %s still exists after the replacement", name)
			}
		}
		return nil
	}
}

// testAccAuditTargetResetOutOfBand removes the target through the admin API
// behind Terraform's back, so the following refresh notices the drift.
func testAccAuditTargetResetOutOfBand(name string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		if err := client.ResetAuditTarget(context.Background(), "audit_webhook", name); err != nil {
			return fmt.Errorf("failed to reset audit target %s out of band: %w", name, err)
		}
		return nil
	}
}

func testAccCheckAuditTargetDestroy(s *terraform.State) error {
	client := testAccRustClient()
	targets, err := client.ListAuditTargets(context.Background())
	if err != nil {
		return err
	}
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_audit_target" {
			continue
		}
		name := rs.Primary.Attributes["target_name"]
		for _, target := range targets {
			if strings.EqualFold(target.AccountID, name) {
				return fmt.Errorf("audit target %s still exists", name)
			}
		}
	}
	return nil
}

// Migrated from main's read/delete/create error-path work.
func TestAuditTargetResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantState   bool
		wantSummary string
		wantDetail  string
	}{
		{
			// The list endpoint answers 200 with an empty audit_endpoints
			// array for a target that is gone, which is how the resource
			// learns the target no longer exists.
			name: "target absent from the list is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "audit/target/list",
				status: http.StatusOK, body: `{"audit_endpoints":[]}`,
			}},
			wantRemoved: true,
		},
		{
			name: "listed target keeps state and mirrors its health",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "audit/target/list", status: http.StatusOK,
				body: `{"audit_endpoints":[{"account_id":"tf-test","service":"webhook","status":"online","health_state":"online","health_reason":"reachable"}]}`,
			}},
			wantState: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantState:   true,
			wantSummary: "Error listing audit targets",
			wantDetail:  "Could not list audit targets, unexpected error: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewAuditTargetResource(), client)

			resp := h.read(&auditTargetResourceModel{
				TargetType: types.StringValue("audit_webhook"),
				TargetName: types.StringValue("tf-test"),
			})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertDiagnosticDetail(t, resp.Diagnostics, "AccessDenied")
				assertStatePresent(t, resp.State)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)
			assertStatePresent(t, resp.State)
			assertStateString(t, resp.State, path.Root("health_state"), "online")
			assertStateString(t, resp.State, path.Root("status"), "online")
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// A reset of a target that is already gone is 200 OK, so a
			// repeated destroy stays a no-op instead of failing the apply.
			name:   "already removed target is not an error",
			routes: []requestRoute{{method: http.MethodDelete, pathContains: "/reset", status: http.StatusOK}},
		},
		{
			// A disabled audit module is a real server error, not a missing
			// target, and must reach the user.
			name: "real server error is reported",
			routes: []requestRoute{{
				method: http.MethodDelete, pathContains: "/reset", status: http.StatusBadRequest,
				body: s3ErrorXML("InvalidRequest", "audit module is disabled; enable the audit module first"),
			}},
			wantSummary: "Error resetting audit target",
			wantDetail:  "Could not reset audit target, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewAuditTargetResource(), client)

			resp := h.delete(&auditTargetResourceModel{
				TargetType: types.StringValue("audit_webhook"),
				TargetName: types.StringValue("tf-test"),
			})

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, "audit module is disabled")
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		plan        *auditTargetResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			// A block that does not match target_type is rejected before any
			// API call, so the server never sees a half-built target.
			name: "mismatched configuration block writes no state",
			plan: &auditTargetResourceModel{
				TargetType: types.StringValue("audit_webhook"),
				TargetName: types.StringValue("tf-test"),
				Kafka: &targetKafkaConfigModel{
					Brokers: types.StringValue("broker:9092"),
					Topic:   types.StringValue("tf-audit"),
				},
			},
			wantSummary: "Invalid audit target configuration",
			wantDetail:  `target type "audit_webhook" requires the webhook_config block`,
		},
		{
			name: "unsupported target type is rejected by the server",
			routes: []requestRoute{{
				method: http.MethodPut, pathContains: "audit/target/audit_bogus", status: http.StatusBadRequest,
				body: s3ErrorXML("InvalidArgument", "unsupported audit target type: 'audit_bogus'"),
			}},
			plan: &auditTargetResourceModel{
				TargetType: types.StringValue("audit_bogus"),
				TargetName: types.StringValue("tf-test"),
			},
			wantSummary: "Error creating audit target",
			wantDetail:  "unsupported audit target type: 'audit_bogus'",
		},
		{
			// The flat legacy path with no endpoint at all is a key/value set
			// the server refuses.
			name: "invalid key/value set is rejected by the server",
			routes: []requestRoute{{
				method: http.MethodPut, pathContains: "audit/target/audit_webhook", status: http.StatusBadRequest,
				body: s3ErrorXML("InvalidArgument", "invalid key values: endpoint is required"),
			}},
			plan: &auditTargetResourceModel{
				TargetType: types.StringValue("audit_webhook"),
				TargetName: types.StringValue("tf-test"),
			},
			wantSummary: "Error creating audit target",
			wantDetail:  "invalid key values: endpoint is required",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewAuditTargetResource(), client)

			resp := h.create(tc.plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
			// A failed create must not go on to list and refresh health.
			rec.assertNoRequest(t, "audit/target/list")
		})
	}

	t.Run("validate config: mismatched block is rejected before planning", func(t *testing.T) {
		h := newCRUDHarness(t, NewAuditTargetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.validateConfig(&auditTargetResourceModel{
			TargetType: types.StringValue("audit_kafka"),
			TargetName: types.StringValue("tf-test"),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid audit target configuration",
			`target type "audit_kafka" requires the kafka_config block`)
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewAuditTargetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
