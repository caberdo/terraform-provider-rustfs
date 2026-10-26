// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const websiteConfigurationPayload = `<?xml version="1.0" encoding="UTF-8"?>
<WebsiteConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<IndexDocument><Suffix>index.html</Suffix></IndexDocument>
<ErrorDocument><Key>error.html</Key></ErrorDocument>
<RoutingRules>
<RoutingRule>
<Condition><KeyPrefixEquals>docs/</KeyPrefixEquals></Condition>
<Redirect><ReplaceKeyPrefixWith>documents/</ReplaceKeyPrefixWith></Redirect>
</RoutingRule>
</RoutingRules>
</WebsiteConfiguration>`

func TestBuildWebsiteConfiguration(t *testing.T) {
	plan := bucketWebsiteModel{
		Bucket:              types.StringValue("tf-test"),
		IndexDocumentSuffix: types.StringValue("index.html"),
		ErrorDocumentKey:    types.StringValue("error.html"),
		RoutingRule: []routingRuleModel{{
			Condition: &routingRuleConditionModel{
				KeyPrefixEquals:             types.StringValue("docs/"),
				HttpErrorCodeReturnedEquals: types.StringNull(),
			},
			Redirect: &routingRuleRedirectModel{
				HostName:             types.StringValue("example.com"),
				HttpRedirectCode:     types.StringValue("301"),
				Protocol:             types.StringValue("https"),
				ReplaceKeyPrefixWith: types.StringValue("documents/"),
				ReplaceKeyWith:       types.StringNull(),
			},
		}},
	}

	config := buildWebsiteConfiguration(plan)

	if aws.ToString(config.IndexDocument.Suffix) != "index.html" {
		t.Errorf("index suffix = %q", aws.ToString(config.IndexDocument.Suffix))
	}
	if aws.ToString(config.ErrorDocument.Key) != "error.html" {
		t.Errorf("error key = %q", aws.ToString(config.ErrorDocument.Key))
	}
	if len(config.RoutingRules) != 1 {
		t.Fatalf("expected 1 routing rule, got %d", len(config.RoutingRules))
	}
	rule := config.RoutingRules[0]
	if aws.ToString(rule.Condition.KeyPrefixEquals) != "docs/" {
		t.Errorf("condition prefix = %q", aws.ToString(rule.Condition.KeyPrefixEquals))
	}
	if rule.Condition.HttpErrorCodeReturnedEquals != nil {
		t.Error("null http_error_code_returned_equals must not be sent")
	}
	if aws.ToString(rule.Redirect.ReplaceKeyPrefixWith) != "documents/" {
		t.Errorf("replace prefix = %q", aws.ToString(rule.Redirect.ReplaceKeyPrefixWith))
	}
	if rule.Redirect.Protocol != s3types.ProtocolHttps {
		t.Errorf("redirect protocol = %q", rule.Redirect.Protocol)
	}
}

func TestBuildWebsiteConfigurationRedirectAll(t *testing.T) {
	config := buildWebsiteConfiguration(bucketWebsiteModel{
		Bucket: types.StringValue("tf-test"),
		RedirectAllRequestsTo: &redirectAllRequestsToModel{
			HostName: types.StringValue("example.com"),
			Protocol: types.StringValue("https"),
		},
	})

	if config.RedirectAllRequestsTo == nil {
		t.Fatal("expected a redirect-all configuration")
	}
	if aws.ToString(config.RedirectAllRequestsTo.HostName) != "example.com" {
		t.Errorf("host = %q", aws.ToString(config.RedirectAllRequestsTo.HostName))
	}
	if config.IndexDocument != nil || config.ErrorDocument != nil || len(config.RoutingRules) != 0 {
		t.Error("a redirect-all configuration must not carry documents or rules")
	}
}

func TestBucketWebsiteResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewBucketWebsiteResource())
	for _, want := range []string{"bucket", "id", "index_document_suffix", "error_document_key", "redirect_all_requests_to"} {
		if _, ok := sch.GetAttributes()[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
	if _, ok := sch.GetBlocks()["routing_rule"]; !ok {
		t.Error("missing routing_rule block")
	}
	for name, attr := range sch.GetAttributes() {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestBucketWebsiteResourceMetadata(t *testing.T) {
	resp := &resource.MetadataResponse{}
	NewBucketWebsiteResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_website" {
		t.Errorf("expected rustfs_bucket_website, got %s", resp.TypeName)
	}
}

func TestBucketWebsiteResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("create writes the configuration", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusOK})
		h := newCRUDHarness(t, NewBucketWebsiteResource(), client)

		resp := h.create(&bucketWebsiteModel{
			Bucket:              types.StringValue("tf-test"),
			IndexDocumentSuffix: types.StringValue("index.html"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "tf-test")
		recorder.assertRequest(t, "PUT")
	})

	t.Run("create failure reports the server error and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusConflict,
			body:   s3ErrorXML("InvalidRequest", "a redirect cannot carry documents"),
		})
		h := newCRUDHarness(t, NewBucketWebsiteResource(), client)
		plan := &bucketWebsiteModel{Bucket: types.StringValue("tf-test")}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating bucket website configuration", "a redirect cannot carry documents")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("read mirrors the documents and routing rules", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodGet, status: http.StatusOK, body: websiteConfigurationPayload})
		h := newCRUDHarness(t, NewBucketWebsiteResource(), client)

		resp := h.read(&bucketWebsiteModel{
			Bucket:      types.StringValue("tf-test"),
			Id:          types.StringValue("tf-test"),
			RoutingRule: []routingRuleModel{{}},
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("index_document_suffix"), "index.html")
		assertStateString(t, resp.State, path.Root("error_document_key"), "error.html")

		var got bucketWebsiteModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
		if got.RedirectAllRequestsTo != nil {
			t.Errorf("redirect_all_requests_to = %+v, want none", got.RedirectAllRequestsTo)
		}
		if len(got.RoutingRule) != 1 || got.RoutingRule[0].Condition == nil || got.RoutingRule[0].Redirect == nil {
			t.Fatalf("routing rules not flattened: %+v", got.RoutingRule)
		}
		if got.RoutingRule[0].Condition.KeyPrefixEquals.ValueString() != "docs/" {
			t.Errorf("condition prefix = %q", got.RoutingRule[0].Condition.KeyPrefixEquals.ValueString())
		}
		if got.RoutingRule[0].Redirect.ReplaceKeyPrefixWith.ValueString() != "documents/" {
			t.Errorf("replace prefix = %q", got.RoutingRule[0].Redirect.ReplaceKeyPrefixWith.ValueString())
		}
	})

	t.Run("read removes a bucket without a website configuration", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet,
			status: http.StatusNotFound,
			body:   s3ErrorXML("NoSuchWebsiteConfiguration", "no website here"),
		})
		h := newCRUDHarness(t, NewBucketWebsiteResource(), client)

		resp := h.read(&bucketWebsiteModel{Bucket: types.StringValue("tf-test")})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("read reports a server error and keeps state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "nope"),
		})
		h := newCRUDHarness(t, NewBucketWebsiteResource(), client)

		resp := h.read(&bucketWebsiteModel{Bucket: types.StringValue("tf-test")})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading bucket website configuration", "nope")
		assertStatePresent(t, resp.State)
	})

	t.Run("update writes the new configuration", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusOK})
		h := newCRUDHarness(t, NewBucketWebsiteResource(), client)

		resp := h.update(
			&bucketWebsiteModel{Bucket: types.StringValue("tf-test"), IndexDocumentSuffix: types.StringValue("home.html")},
			&bucketWebsiteModel{Bucket: types.StringValue("tf-test"), IndexDocumentSuffix: types.StringValue("index.html")},
		)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("index_document_suffix"), "home.html")
		recorder.assertRequest(t, "PUT")
	})

	t.Run("update failure is reported", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusConflict,
			body:   s3ErrorXML("InvalidRequest", "no update"),
		})
		h := newCRUDHarness(t, NewBucketWebsiteResource(), client)

		resp := h.update(
			&bucketWebsiteModel{Bucket: types.StringValue("tf-test"), IndexDocumentSuffix: types.StringValue("home.html")},
			&bucketWebsiteModel{Bucket: types.StringValue("tf-test"), IndexDocumentSuffix: types.StringValue("index.html")},
		)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket website configuration", "no update")
	})

	t.Run("delete reports a server error", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodDelete,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no delete"),
		})
		h := newCRUDHarness(t, NewBucketWebsiteResource(), client)

		resp := h.delete(&bucketWebsiteModel{Bucket: types.StringValue("tf-test")})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error deleting bucket website configuration", "no delete")
	})

	t.Run("delete removes the configuration and is idempotent", func(t *testing.T) {
		for name, status := range map[string]int{
			"server removes the configuration": http.StatusNoContent,
			"configuration already absent":     http.StatusNotFound,
		} {
			t.Run(name, func(t *testing.T) {
				client, recorder := routedClient(t, requestRoute{
					method: http.MethodDelete,
					status: status,
					body:   s3ErrorXML("NoSuchWebsiteConfiguration", "gone"),
				})
				h := newCRUDHarness(t, NewBucketWebsiteResource(), client)

				resp := h.delete(&bucketWebsiteModel{
					Bucket:              types.StringValue("tf-test"),
					IndexDocumentSuffix: types.StringValue("index.html"),
				})

				requireNoDiagnostics(t, resp.Diagnostics)
				recorder.assertRequest(t, "DELETE")
			})
		}
	})
}

func TestAccBucketWebsite_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-website-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_website.test"

	tfresource.ParallelTest(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []tfresource.TestStep{
			{
				Config: testAccBucketWebsiteConfig(name, "index.html", "error.html"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "index_document_suffix", "index.html"),
					tfresource.TestCheckResourceAttr(resourceName, "error_document_key", "error.html"),
					tfresource.TestCheckResourceAttr(resourceName, "routing_rule.#", "1"),
					tfresource.TestCheckResourceAttr(resourceName, "routing_rule.0.condition.key_prefix_equals", "docs/"),
					tfresource.TestCheckResourceAttr(resourceName, "routing_rule.0.redirect.replace_key_prefix_with", "documents/"),
					testAccCheckBucketStillExists(name),
				),
			},
			{
				// In place, with an empty follow-up plan: the server echoes the
				// documents and rules Put accepted.
				Config: testAccBucketWebsiteConfig(name, "home.html", "oops.html"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "index_document_suffix", "home.html"),
					tfresource.TestCheckResourceAttr(resourceName, "error_document_key", "oops.html"),
					testAccCheckBucketStillExists(name),
				),
			},
			{
				Config:   testAccBucketWebsiteConfig(name, "home.html", "oops.html"),
				PlanOnly: true,
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func TestAccBucketWebsite_redirectAll(t *testing.T) {
	name := fmt.Sprintf("tf-test-website-redirect-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_website.test"

	tfresource.ParallelTest(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []tfresource.TestStep{
			{
				Config: testAccBucketWebsiteRedirectConfig(name, "https"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "redirect_all_requests_to.host_name", "example.com"),
					tfresource.TestCheckResourceAttr(resourceName, "redirect_all_requests_to.protocol", "https"),
				),
			},
			{
				Config:   testAccBucketWebsiteRedirectConfig(name, "https"),
				PlanOnly: true,
			},
		},
	})
}

func testAccBucketWebsiteRedirectConfig(name, protocol string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "redirect" {
  name = "%s"
}

resource "rustfs_bucket_website" "test" {
  bucket = rustfs_bucket.redirect.name

  redirect_all_requests_to = {
    host_name = "example.com"
    protocol  = "%s"
  }
}
`, name, protocol)
}

func testAccBucketWebsiteConfig(name, index, errorDocument string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_website" "test" {
  bucket                = rustfs_bucket.test.name
  index_document_suffix = "%s"
  error_document_key    = "%s"

  routing_rule {
    condition {
      key_prefix_equals = "docs/"
    }

    redirect {
      replace_key_prefix_with = "documents/"
    }
  }
}
`, name, index, errorDocument)
}

func TestBucketWebsiteResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewBucketWebsiteResource().(*bucketWebsiteResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewBucketWebsiteResource().(*bucketWebsiteResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Resource Configure Type")
	})
}

func TestBuildWebsiteConfigurationMinimal(t *testing.T) {
	// A rule without condition or redirect is passed through empty; the server
	// rejects it, but the builder must not invent values or panic.
	config := buildWebsiteConfiguration(bucketWebsiteModel{
		Bucket: types.StringValue("tf-test"),
		RedirectAllRequestsTo: &redirectAllRequestsToModel{
			HostName: types.StringValue("example.com"),
			Protocol: types.StringNull(),
		},
		RoutingRule: []routingRuleModel{{}},
	})

	if config.RedirectAllRequestsTo.Protocol != "" {
		t.Errorf("null protocol must not be sent, got %q", config.RedirectAllRequestsTo.Protocol)
	}
	if len(config.RoutingRules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(config.RoutingRules))
	}
	if config.RoutingRules[0].Condition != nil || config.RoutingRules[0].Redirect != nil {
		t.Errorf("empty rule must stay empty, got %+v", config.RoutingRules[0])
	}
}

func TestFlattenWebsiteConfiguration(t *testing.T) {
	t.Run("an empty answer clears every field", func(t *testing.T) {
		model := bucketWebsiteModel{
			Bucket:              types.StringValue("tf-test"),
			IndexDocumentSuffix: types.StringValue("index.html"),
			ErrorDocumentKey:    types.StringValue("error.html"),
			RedirectAllRequestsTo: &redirectAllRequestsToModel{
				HostName: types.StringValue("example.com"),
			},
			RoutingRule: []routingRuleModel{{}},
		}

		flattenWebsiteConfiguration(&s3.GetBucketWebsiteOutput{}, &model)

		if !model.IndexDocumentSuffix.IsNull() || !model.ErrorDocumentKey.IsNull() {
			t.Errorf("documents were not cleared: %+v", model)
		}
		if model.RedirectAllRequestsTo != nil {
			t.Errorf("redirect was not cleared: %+v", model.RedirectAllRequestsTo)
		}
		if len(model.RoutingRule) != 0 {
			t.Errorf("rules were not cleared: %+v", model.RoutingRule)
		}
	})

	t.Run("a redirect-all answer flattens host and protocol", func(t *testing.T) {
		model := bucketWebsiteModel{Bucket: types.StringValue("tf-test")}

		flattenWebsiteConfiguration(&s3.GetBucketWebsiteOutput{
			RedirectAllRequestsTo: &s3types.RedirectAllRequestsTo{
				HostName: aws.String("example.com"),
				Protocol: s3types.ProtocolHttps,
			},
		}, &model)

		if model.RedirectAllRequestsTo == nil {
			t.Fatal("redirect was not flattened")
		}
		if model.RedirectAllRequestsTo.HostName.ValueString() != "example.com" {
			t.Errorf("host = %q", model.RedirectAllRequestsTo.HostName.ValueString())
		}
		if model.RedirectAllRequestsTo.Protocol.ValueString() != "https" {
			t.Errorf("protocol = %q", model.RedirectAllRequestsTo.Protocol.ValueString())
		}
	})
}
