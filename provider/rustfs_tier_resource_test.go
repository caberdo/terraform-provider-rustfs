// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const tierResourceName = "rustfs_tier.test"

func TestTierResourceSchema(t *testing.T) {
	r := NewTierResource()
	sch := resourceSchemaOf(t, r)

	attrs := sch.GetAttributes()
	if len(attrs) != 3 {
		t.Errorf("expected 3 attributes, got %d: %v", len(attrs), attrs)
	}

	name, ok := attrs["name"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("name is not a StringAttribute")
	}
	if !name.Required || name.Optional || name.Computed {
		t.Errorf("name must be Required only: %+v", name)
	}
	if !hasRequiresReplace(name.PlanModifiers) {
		t.Error("name must force recreation, the server keys tiers by name")
	}

	tierType, ok := attrs["tier_type"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("tier_type is not a StringAttribute")
	}
	if !tierType.Required || tierType.Optional || tierType.Computed {
		t.Errorf("tier_type must be Required only: %+v", tierType)
	}
	if !hasRequiresReplace(tierType.PlanModifiers) {
		t.Error("tier_type must force recreation, the server cannot change a tier's type")
	}

	configJSON, ok := attrs["config_json"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("config_json is not a StringAttribute")
	}
	if !configJSON.Required || configJSON.Optional || configJSON.Computed {
		t.Errorf("config_json must be Required only: %+v", configJSON)
	}
	if !configJSON.Sensitive {
		t.Error("config_json carries backend credentials and must be Sensitive")
	}
	if hasRequiresReplace(configJSON.PlanModifiers) {
		t.Error("config_json is editable through EditTier and must not force recreation")
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description, tfplugindocs would publish an empty row", name)
		}
	}
}

func TestTierResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewTierResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_tier" {
		t.Errorf("expected rustfs_tier, got %s", resp.TypeName)
	}
}

func TestTierResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewTierResource().(*TierResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewTierResource().(*TierResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected a diagnostic for unexpected provider data")
		}
		if got := resp.Diagnostics[0].Summary(); got != "Unexpected Resource Configure Type" {
			t.Errorf("unexpected diagnostic summary: %s", got)
		}
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestTierResourceCreate(t *testing.T) {
	t.Run("adds the tier", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		model := tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue(`{"type":"rustfs","rustfs":{"name":"S3COLD"}}`),
		}
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, model)}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodPut || request.Path != "/rustfs/admin/v3/tier" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if string(request.Body) != model.ConfigJson.ValueString() {
			t.Errorf("config_json was not sent verbatim: %s", request.Body)
		}

		var got tierResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Name.ValueString() != "S3COLD" || got.TierType.ValueString() != "s3" {
			t.Errorf("state was not written from the plan: %+v", got)
		}
	})

	t.Run("rejects invalid JSON before calling the API", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue("not json"),
		})
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: plan}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid JSON")
		if got := recorder.count(); got != 0 {
			t.Errorf("expected no admin API request, got %d", got)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, "<Error><Code>TierAddFailed</Code><Message>tier add failed</Message></Error>")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue("{}"),
		})
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: plan}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error adding tier")
		if !strings.Contains(diagnostic.Detail(), "TierAddFailed") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

// TestTierResourceRead was deleted with #74: it pinned Read as a no-op that
// never calls the admin API, which is the behaviour the issue requires to
// change (a tier removed outside Terraform stayed in state forever).
// TestTierResourceLifecyclePaths covers the new contract: the read refreshes
// tier_type from the tier listing, keeps a tier the exact-name check still
// holds, removes a tier the server no longer has, and reports a server error.

func TestTierResourceUpdate(t *testing.T) {
	t.Run("edits the tier", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue(`{"type":"rustfs","rustfs":{"name":"S3COLD","region":"us-east-01"}}`),
		}
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  resourcePlan(t, sch, plan),
			State: resourceState(t, sch, tierResourceModel{}),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodPost || request.Path != "/rustfs/admin/v3/tier/S3COLD" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if got, want := string(request.Body), plan.ConfigJson.ValueString(); got != want {
			t.Errorf("the edited configuration did not reach the server: body = %q, want %q", got, want)
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue("{"),
		}
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  resourcePlan(t, sch, plan),
			State: resourceState(t, sch, tierResourceModel{}),
		}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid JSON")
		if got := recorder.count(); got != 0 {
			t.Errorf("expected no admin API request, got %d", got)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, "<Error><Code>TierEditFailed</Code></Error>")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := tierResourceModel{
			Name:       types.StringValue("S3COLD"),
			TierType:   types.StringValue("s3"),
			ConfigJson: types.StringValue("{}"),
		}
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, tierResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  resourcePlan(t, sch, plan),
			State: resourceState(t, sch, tierResourceModel{}),
		}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Error editing tier")
	})
}

func TestTierResourceDelete(t *testing.T) {
	t.Run("removes the tier", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		state := tierResourceModel{Name: types.StringValue("S3COLD")}
		resp := &frameworkresource.DeleteResponse{}
		r.Delete(context.Background(), frameworkresource.DeleteRequest{State: resourceState(t, sch, state)}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodDelete || request.Path != "/rustfs/admin/v3/tier/S3COLD" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusConflict, "<Error><Code>XMinioAdminTierBackendInUse</Code></Error>")
		r := NewTierResource().(*TierResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		resp := &frameworkresource.DeleteResponse{}
		r.Delete(context.Background(), frameworkresource.DeleteRequest{
			State: resourceState(t, sch, tierResourceModel{Name: types.StringValue("S3COLD")}),
		}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Error removing tier")
	})
}

func TestTierResourceImportState(t *testing.T) {
	r := NewTierResource().(*TierResource)
	sch := resourceSchemaOf(t, r)

	resp := &frameworkresource.ImportStateResponse{State: resourceState(t, sch, tierResourceModel{})}
	r.ImportState(context.Background(), frameworkresource.ImportStateRequest{ID: "S3COLD"}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got types.String
	if diags := resp.State.GetAttribute(context.Background(), path.Root("name"), &got); diags.HasError() {
		t.Fatalf("reading the imported name: %v", diags)
	}
	if got.ValueString() != "S3COLD" {
		t.Errorf("expected the import ID to land in name, got %q", got.ValueString())
	}
}

// TestTierResourceReadPopulatesImportedTierType reproduces #116: an import
// seeds only the name, so tier_type starts empty even though it is Required
// and RequiresReplace. Read must fill it from the server listing, otherwise
// Terraform plans to delete and recreate a tier that is already there.
func TestTierResourceReadPopulatesImportedTierType(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{method: http.MethodGet, pathContains: "/v3/tier/COLD", status: http.StatusOK},
		requestRoute{method: http.MethodGet, pathContains: "/v3/tier", status: http.StatusOK, body: `[{"type":"rustfs","rustfs":{"name":"COLD"}}]`},
	)
	h := newCRUDHarness(t, NewTierResource(), client)

	resp := h.read(&tierResourceModel{Name: types.StringValue("COLD")})

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)
	assertStateString(t, resp.State, path.Root("name"), "COLD")
	assertStateString(t, resp.State, path.Root("tier_type"), "rustfs")
}

// TestTierResourceReadSkipsListingWhenTypeIsKnown pins that a normal refresh
// uses the exact-name check only: the listing is needed for an import, not for
// every tier on every plan.
func TestTierResourceReadSkipsListingWhenTypeIsKnown(t *testing.T) {
	client, recorder := routedClient(t, requestRoute{
		method:       http.MethodGet,
		pathContains: "/v3/tier/COLD",
		status:       http.StatusOK,
	})
	h := newCRUDHarness(t, NewTierResource(), client)

	resp := h.read(&tierResourceModel{
		Name:       types.StringValue("COLD"),
		TierType:   types.StringValue("s3"),
		ConfigJson: types.StringValue(`{"name":"COLD"}`),
	})

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)
	assertStateString(t, resp.State, path.Root("tier_type"), "s3")
	if want := []string{"GET /rustfs/admin/v3/tier/COLD"}; !slices.Equal(recorder.requests, want) {
		t.Errorf("Read made %v, want only the exact-name check %v", recorder.requests, want)
	}
}

// Tier acceptance coverage. A tier is only created when the server can probe a
// reachable remote backend, so the test needs a second RustFS to point at and
// skips when the environment does not provide one. The native `rustfs` tier
// type is the one usable target here: the S3-compatible types additionally
// require credentials the remote accepts, which a two-container CI deployment
// cannot supply.

func TestAccTierResource(t *testing.T) {
	// The precheck runs here as well as in the test case below: the endpoint
	// environment is read before resource.Test, so an unconfigured environment
	// must skip the test rather than fail reading it.
	tierTestPreCheck(t)

	tierEndpoint := tierTestEndpoint(t)
	s3Endpoint := tierTestS3Endpoint(t)
	name := fmt.Sprintf("TFACCTIER%d", acctest.RandInt())
	replacementName := fmt.Sprintf("TFACCTIERR%d", acctest.RandInt())
	bucket := fmt.Sprintf("tf-acc-tier-%d", acctest.RandInt())

	// The tier probe requires the target bucket to exist, and the provider's
	// aliased-provider route is not available here: RUSTFS_ENDPOINT overrides a
	// configured endpoint, so a second provider block would still talk to the
	// deployment under test. Create the bucket with the same S3 client the
	// acceptance bucket tests use instead.
	tierBackendBucket := tierTestBucket{endpoint: s3Endpoint, name: bucket}
	tierBackendBucket.create(t)
	defer tierBackendBucket.delete(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTierDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccTierConfig(tierEndpoint, name, bucket),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "name", name),
					resource.TestCheckResourceAttr(tierResourceName, "tier_type", "rustfs"),
					resource.TestCheckResourceAttrSet(tierResourceName, "config_json"),
					testAccCheckTierRegistered(t, name),
				),
			},
			{
				// In-place update: config_json is the only mutable attribute.
				// The provider sends the new backend to the server and records
				// it; the tier stays registered. RustFS keeps reporting the
				// configuration a tier was created with (and redacts secretKey),
				// so the edited value cannot be read back from this server -
				// TestTierResourceUpdate pins the payload that reaches the wire
				// instead, and the revert plus the empty plan below prove the
				// plan converges.
				Config: testAccTierConfigWithRegion(tierEndpoint, name, bucket, tierTestUpdatedRegion),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "name", name),
					resource.TestCheckResourceAttrSet(tierResourceName, "config_json"),
					testAccCheckTierRegistered(t, name),
				),
			},
			{
				// Revert the in-place update.
				Config: testAccTierConfig(tierEndpoint, name, bucket),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "name", name),
					testAccCheckTierRegistered(t, name),
				),
			},
			{
				// Computed consistency: the state must match the configuration,
				// with no computed attribute causing a perpetual diff.
				Config:   testAccTierConfig(tierEndpoint, name, bucket),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Replacement plan for "tier_type", one of the two
				// RequiresReplace attributes. Plan-only: the replacement of the
				// name below is the one that is applied, so the server is left
				// with a single tier and never has to support this type.
				Config:             testAccTierConfigWithType(tierEndpoint, name, bucket, tierTestReplacementType),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(tierResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Replacement plan for "name", the other RequiresReplace
				// attribute.
				Config:             testAccTierConfig(tierEndpoint, replacementName, bucket),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(tierResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement so the destroy-and-create path really
				// runs: the old tier is gone and the new one is registered on
				// the server.
				Config: testAccTierConfig(tierEndpoint, replacementName, bucket),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "name", replacementName),
					testAccCheckTierRegistered(t, replacementName),
					testAccCheckTierAbsent(t, name),
				),
			},
			{
				// Out-of-band drift: the tier is edited straight through the
				// admin API, behind Terraform's back. Read refreshes tier_type
				// from the listing but does not round-trip config_json (the
				// server redacts credentials and keeps the value a tier was
				// created with), so the follow-up plan stays empty by design;
				// the check proves the server really moved.
				Config:             testAccTierConfig(tierEndpoint, replacementName, bucket),
				ExpectNonEmptyPlan: false,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckTierRegistered(t, replacementName),
					testAccMutateTierOutOfBand(t, tierEndpoint, replacementName, bucket),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// ImportStatePassthroughID maps the tier name and Read fills
				// tier_type from the server listing, so importing a tier that
				// already exists must not plan a replacement (#116). Only
				// config_json stays absent from the imported state: the server
				// redacts its credentials, so the configuration has to be
				// re-supplied by an in-place update.
				ResourceName:                         tierResourceName,
				ImportState:                          true,
				ImportStateId:                        replacementName,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"config_json"},
				ExpectNonEmptyPlan:                   true,
				ImportPlanChecks: resource.ImportPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(tierResourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(tierResourceName, "name", replacementName),
					resource.TestCheckResourceAttr(tierResourceName, "tier_type", "rustfs"),
				),
			},
		},
	})
}

// tierTestUpdatedRegion is the region the in-place update step switches to: a
// value the tier listing never echoes, like every other config_json field.
const tierTestUpdatedRegion = "eu-west-01"

// tierTestReplacementType is the tier type used for the plan-only replacement
// check. It is never applied, so the server under test never has to support it.
const tierTestReplacementType = "minio"

// testAccCheckTierRegistered asserts the tier is present in the server's tier
// listing, so a state assertion cannot pass for a tier the server never got.
func testAccCheckTierRegistered(t *testing.T, name string) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client := testAccRustClient()
		stats, err := client.TierStats(context.Background())
		if err != nil {
			return fmt.Errorf("listing tiers: %w", err)
		}
		if _, ok := stats[name]; !ok {
			return fmt.Errorf("tier %s is not registered on the server (have %v)", name, tierNames(stats))
		}
		return nil
	}
}

// testAccCheckTierAbsent asserts the tier is gone from the server's listing.
// The server reconciles a removal in the background, so the check polls.
func testAccCheckTierAbsent(t *testing.T, name string) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client := testAccRustClient()
		deadline := time.Now().Add(tierMutationAttempts * tierMutationInterval)
		for {
			stats, err := client.TierStats(context.Background())
			if err != nil {
				return fmt.Errorf("listing tiers: %w", err)
			}
			if _, ok := stats[name]; !ok {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("tier %s is still registered on the server (have %v)", name, tierNames(stats))
			}
			time.Sleep(tierMutationInterval)
		}
	}
}

// tierNames lists the tier names the server reports, for failure messages.
func tierNames(stats map[string]rustfs.TierStat) []string {
	names := make([]string, 0, len(stats))
	for name := range stats {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// testAccMutateTierOutOfBand edits the tier through the admin API, behind
// Terraform's back, and fails unless the edit is accepted.
func testAccMutateTierOutOfBand(t *testing.T, tierEndpoint, name, bucket string) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client := testAccRustClient()
		config, err := json.Marshal(map[string]any{
			"type": "rustfs",
			"rustfs": map[string]any{
				"name":      name,
				"endpoint":  tierEndpoint,
				"accessKey": os.Getenv("RUSTFS_USER"),
				"secretKey": os.Getenv("RUSTFS_SECRET"),
				"bucket":    bucket,
				"region":    "drifted-01",
			},
		})
		if err != nil {
			return fmt.Errorf("rendering the out-of-band tier configuration: %w", err)
		}

		var editErr error
		for range tierMutationAttempts {
			if editErr = client.EditTier(context.Background(), name, config); editErr == nil {
				t.Logf("edited tier %s out of band", name)
				return nil
			}
			// The server serialises tier mutations: an edit issued while the
			// previous one is still recovering is refused, not lost.
			time.Sleep(tierMutationInterval)
		}
		return fmt.Errorf("out-of-band edit of tier %s: %w", name, editErr)
	}
}

// TestTierTransientMutationErrors pins which refusals the resource retries: the
// two the server answers while a previous tier mutation has not finished
// reconciling, and nothing else.
func TestTierTransientMutationErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil},
		{name: "unrelated", err: errors.New("NoSuchBucket")},
		{name: "tier name taken", err: &rustfs.APIError{Code: "TierNameAlreadyExist", Message: "tier name already exists"}},
		{name: "edit still recovering", err: &rustfs.APIError{Code: "TierEditFailed", Message: "tier edit failed. Remote tier mutation recovery must finish before applying a new tier configuration update"}, want: true},
		{name: "backend in use", err: &rustfs.APIError{Code: "XMinioAdminTierBackendInUse", Message: "Specified remote tier is already in use"}, want: true},
		{name: "unstructured backend in use", err: errors.New("<Error><Code>XMinioAdminTierBackendInUse</Code><Message>Specified remote tier is already in use</Message></Error>"), want: true},
		{name: "unstructured edit recovery", err: errors.New("tier edit failed. Remote tier mutation recovery must finish before applying a new tier configuration update"), want: true},
	}
	for _, tc := range cases {
		if got := isTransientTierMutationError(tc.err); got != tc.want {
			t.Errorf("%s: isTransientTierMutationError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestTierMutationRetry pins the retry itself: a transient refusal is retried
// and a permanent one is reported on the first attempt.
func TestTierMutationRetry(t *testing.T) {
	t.Run("retries a transient refusal", func(t *testing.T) {
		calls := 0
		err := tierMutation(func() error {
			calls++
			if calls < 3 {
				return &rustfs.APIError{Code: "XMinioAdminTierBackendInUse", Message: "Specified remote tier is already in use"}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected 3 attempts, got %d", calls)
		}
	})

	t.Run("reports a permanent error immediately", func(t *testing.T) {
		calls := 0
		wantErr := errors.New("Specified tier type is unsupported")
		err := tierMutation(func() error {
			calls++
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected %v, got %v", wantErr, err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 attempt, got %d", calls)
		}
	})
}

const (
	// tierTestEndpointEnv is the endpoint the RustFS server probes when it adds
	// the tier: a second RustFS, reachable from the server.
	tierTestEndpointEnv = "RUSTFS_TIER_ENDPOINT"
	// tierTestS3EndpointEnv overrides the host:port the test dials for that
	// second RustFS. It defaults to the host:port of RUSTFS_TIER_ENDPOINT,
	// which is correct inside the acceptance compose network; a host-based
	// local run sets it to the published port.
	tierTestS3EndpointEnv = "RUSTFS_TIER_S3_ENDPOINT"
)

// tierTestEndpoint returns the endpoint a tier can point at, failing when the
// variable is set but does not look like a URL.
func tierTestEndpoint(t *testing.T) string {
	t.Helper()
	full := os.Getenv(tierTestEndpointEnv)
	if !strings.HasPrefix(full, "http://") && !strings.HasPrefix(full, "https://") {
		t.Fatalf("%s must be a URL such as http://rustfs-peer:9000, got %q", tierTestEndpointEnv, full)
	}
	return full
}

// tierTestS3Endpoint returns the host:port of the tier backend as the test
// process reaches it.
func tierTestS3Endpoint(t *testing.T) string {
	t.Helper()
	if override := os.Getenv(tierTestS3EndpointEnv); override != "" {
		return override
	}
	return strings.TrimPrefix(strings.TrimPrefix(tierTestEndpoint(t), "http://"), "https://")
}

// tierTestPreCheck skips the test when no tier backend is configured: without a
// second endpoint the server cannot probe a tier, and pointing a tier at the
// deployment itself is refused ("probe outcome is uncertain").
func tierTestPreCheck(t *testing.T) {
	testAccPreCheck(t)
	if os.Getenv(tierTestEndpointEnv) == "" {
		t.Skipf("%s is not set, skipping: no tier backend is available for the server to probe", tierTestEndpointEnv)
	}
}

// tierTestBucket is the bucket the tier points at, on the tier backend.
type tierTestBucket struct {
	endpoint string
	name     string
}

func (b tierTestBucket) create(t *testing.T) {
	t.Helper()
	client, err := newTestS3Client(b.endpoint, os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"))
	if err != nil {
		t.Fatalf("building the S3 client for the tier backend %s: %v", b.endpoint, err)
	}
	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(b.name)}); err != nil {
		t.Fatalf("creating the tier backend bucket %s on %s: %v", b.name, b.endpoint, err)
	}
}

func (b tierTestBucket) delete(t *testing.T) {
	t.Helper()
	client, err := newTestS3Client(b.endpoint, os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"))
	if err != nil {
		t.Logf("could not build the S3 client to delete the tier backend bucket %s: %v", b.name, err)
		return
	}
	if _, err := client.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(b.name)}); err != nil {
		t.Logf("could not delete the tier backend bucket %s: %v", b.name, err)
	}
}

// testAccCheckTierDestroy requires every tier the fixture created to be gone.
// The admin API answers TierNotFound for a name it does not hold. A server that
// does not implement the verify route at all (501 NotImplemented, 405) cannot
// confirm the tier's absence and is not evidence of a leak, so that passes too;
// an implemented route that still answers for the name fails.
func testAccCheckTierDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_tier" {
			continue
		}
		name := rs.Primary.Attributes["name"]
		if name == "" {
			continue
		}

		res, err := client.DoDirectRequest(context.Background(), rustfs.RequestData{
			Method: http.MethodGet,
			// DoDirectRequest resolves against the host root, so the admin
			// prefix and version the production client builds in adminURL are
			// part of the path here.
			RelPath: "rustfs/admin/v3/tier/" + name,
		})
		if err != nil {
			if rustfs.IsAPIErrorCode(err, "TierNotFound", "NoSuchTier") ||
				rustfs.IsAPIErrorCode(err, "NotImplemented", "MethodNotAllowed") ||
				rustfs.IsUnsupportedFeature(err) {
				continue
			}
			return fmt.Errorf("error checking tier %s destruction: %w", name, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		return fmt.Errorf("tier %s still exists", name)
	}
	return nil
}

func testAccTierConfig(tierEndpoint, name, bucket string) string {
	return testAccTierConfigWith(tierEndpoint, name, bucket, "rustfs", "us-east-01")
}

// testAccTierConfigWithRegion varies the backend's region, which is the only
// way to change config_json without changing an attribute that forces a
// replacement.
func testAccTierConfigWithRegion(tierEndpoint, name, bucket, region string) string {
	return testAccTierConfigWith(tierEndpoint, name, bucket, "rustfs", region)
}

// testAccTierConfigWithType varies tier_type, which carries RequiresReplace().
func testAccTierConfigWithType(tierEndpoint, name, bucket, tierType string) string {
	return testAccTierConfigWith(tierEndpoint, name, bucket, tierType, "us-east-01")
}

func testAccTierConfigWith(tierEndpoint, name, bucket, tierType, region string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_tier" "test" {
  name      = %q
  tier_type = %q
  config_json = jsonencode({
    type = "rustfs"
    rustfs = {
      name      = %q
      endpoint  = %q
      accessKey = %q
      secretKey = %q
      bucket    = %q
      region    = %q
    }
  })
}
`, name, tierType, name, tierEndpoint, os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"), bucket, region)
}

// TestTierResourceLifecyclePaths covers the paths the acceptance suite cannot
// reach with a live server: a tier that disappeared behind Terraform's back, a
// delete of a tier that is already gone, and every failing API call in Create,
// Read and Delete.
func TestTierResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name            string
		routes          []requestRoute
		missingTierType bool
		wantRemoved     bool
		wantSummary     string
		wantDetail      string
		wantTierType    string
	}{
		{
			// Probed on 1.0.1 and latest: GET tier/<name> answers 500
			// TierNotFound for a tier the server does not hold.
			name:        "TierNotFound is removed from state",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("TierNotFound", "tier not found!")}},
			wantRemoved: true,
		},
		{
			name:       "configured tier stays in state",
			routes:     []requestRoute{{status: http.StatusOK}},
			wantDetail: "",
		},
		{
			// An import seeds only name, so the type is missing. The listing
			// supplies it, which keeps Terraform from planning a replacement
			// of a tier the server already holds (#116).
			name:            "import fills the backend type",
			routes:          []requestRoute{{method: http.MethodGet, pathContains: "/v3/tier/COLD", status: http.StatusOK}, {method: http.MethodGet, pathContains: "/v3/tier", status: http.StatusOK, body: `[{"type":"rustfs","rustfs":{"name":"COLD"}}]`}},
			missingTierType: true,
			wantTierType:    "rustfs",
		},
		{
			// A listing failure must not silently leave the type empty: that
			// would reintroduce the destructive replacement plan without an
			// explanation.
			name:            "import reports a listing failure",
			routes:          []requestRoute{{method: http.MethodGet, pathContains: "/v3/tier/COLD", status: http.StatusOK}, {method: http.MethodGet, pathContains: "/v3/tier", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "listing failed")}},
			missingTierType: true,
			wantSummary:     "Error reading tier type",
			wantDetail:      "Could not list tiers: ",
		},
		{
			name:            "import reports a listing without the tier",
			routes:          []requestRoute{{method: http.MethodGet, pathContains: "/v3/tier/COLD", status: http.StatusOK}, {method: http.MethodGet, pathContains: "/v3/tier", status: http.StatusOK, body: `[{"type":"s3","s3":{"name":"OTHER"}}]`}},
			missingTierType: true,
			wantSummary:     "Error reading tier type",
			wantDetail:      `did not report a type for tier "COLD"`,
		},
		{
			// A server that does not implement the verify route cannot answer
			// the existence question, so the last known state is kept rather
			// than planning a destructive recreation.
			name:       "unimplemented route keeps state without a diagnostic",
			routes:     []requestRoute{{status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")}},
			wantDetail: "",
		},
		{
			// The same unsupported route still lets an import learn the type
			// from the listing.
			name:            "unimplemented route still fills a missing type",
			routes:          []requestRoute{{method: http.MethodGet, pathContains: "/v3/tier/COLD", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")}, {method: http.MethodGet, pathContains: "/v3/tier", status: http.StatusOK, body: `[{"type":"rustfs","rustfs":{"name":"COLD"}}]`}},
			missingTierType: true,
			wantTierType:    "rustfs",
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading tier",
			wantDetail:  "Could not read tier: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTierResource(), client)
			state := &tierResourceModel{
				Name:       types.StringValue("COLD"),
				ConfigJson: types.StringValue(`{"name":"COLD"}`),
			}
			if !tc.missingTierType {
				state.TierType = types.StringValue("s3")
			}

			resp := h.read(state)

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertStatePresent(t, resp.State)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)
			assertStatePresent(t, resp.State)
			assertStateString(t, resp.State, path.Root("name"), "COLD")
			if tc.wantTierType != "" {
				assertStateString(t, resp.State, path.Root("tier_type"), tc.wantTierType)
			}
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: DELETE tier/<unknown> answers 200
			// with no error, so deleting an already gone tier is idempotent on
			// the server side.
			name:   "already removed tier is not an error",
			routes: []requestRoute{{status: http.StatusOK, body: ""}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error removing tier",
			wantDetail:  "Could not remove tier: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTierResource(), client)

			resp := h.delete(&tierResourceModel{
				Name:       types.StringValue("COLD"),
				TierType:   types.StringValue("s3"),
				ConfigJson: types.StringValue(`{"name":"COLD"}`),
			})

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		configJSON  string
		wantSummary string
		wantDetail  string
		wantNoCall  bool
	}{
		{
			name:        "invalid JSON is rejected before any API call",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "should not be called")}},
			configJSON:  "not-json",
			wantSummary: "Invalid JSON",
			wantDetail:  "config_json must be valid JSON",
			wantNoCall:  true,
		},
		{
			name:        "rejected tier reports a diagnostic and writes no state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "tier name must be uppercase")}},
			configJSON:  `{"name":"cold","type":"s3"}`,
			wantSummary: "Error adding tier",
			wantDetail:  "Could not add tier: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTierResource(), client)
			plan := &tierResourceModel{
				Name:       types.StringValue("COLD"),
				TierType:   types.StringValue("s3"),
				ConfigJson: types.StringValue(tc.configJSON),
			}

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
			if tc.wantNoCall {
				rec.assertNoRequest(t, "tier")
			}
		})
	}

	updateCases := []struct {
		name        string
		routes      []requestRoute
		configJSON  string
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "invalid JSON is rejected",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "should not be called")}},
			configJSON:  "not-json",
			wantSummary: "Invalid JSON",
			wantDetail:  "config_json must be valid JSON",
		},
		{
			name:        "rejected edit reports a diagnostic",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "unknown tier backend")}},
			configJSON:  `{"name":"COLD","type":"s3"}`,
			wantSummary: "Error editing tier",
			wantDetail:  "Could not edit tier: ",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTierResource(), client)
			plan := &tierResourceModel{
				Name:       types.StringValue("COLD"),
				TierType:   types.StringValue("s3"),
				ConfigJson: types.StringValue(tc.configJSON),
			}

			resp := h.update(plan, plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewTierResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
