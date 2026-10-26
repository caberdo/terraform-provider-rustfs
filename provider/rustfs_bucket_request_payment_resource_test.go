// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const requestPaymentPayload = `<?xml version="1.0" encoding="UTF-8"?>
<RequestPaymentConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Payer>Requester</Payer></RequestPaymentConfiguration>`

func TestBucketRequestPaymentResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewBucketRequestPaymentResource())
	for _, want := range []string{"bucket", "id", "payer"} {
		if _, ok := sch.GetAttributes()[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
	for name, attr := range sch.GetAttributes() {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestBucketRequestPaymentResourceMetadata(t *testing.T) {
	resp := &resource.MetadataResponse{}
	NewBucketRequestPaymentResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_request_payment" {
		t.Errorf("expected rustfs_bucket_request_payment, got %s", resp.TypeName)
	}
}

func TestBucketRequestPaymentResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("create writes the payer", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusOK})
		h := newCRUDHarness(t, NewBucketRequestPaymentResource(), client)

		resp := h.create(&bucketRequestPaymentModel{
			Bucket: types.StringValue("tf-test"),
			Payer:  types.StringValue("Requester"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("payer"), "Requester")
		recorder.assertRequest(t, "PUT")
	})

	t.Run("create failure reports the server error and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no payment config"),
		})
		h := newCRUDHarness(t, NewBucketRequestPaymentResource(), client)
		plan := &bucketRequestPaymentModel{
			Bucket: types.StringValue("tf-test"),
			Payer:  types.StringValue("Requester"),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating bucket request payment configuration", "no payment config")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("read mirrors the payer the server reports", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodGet, status: http.StatusOK, body: requestPaymentPayload})
		h := newCRUDHarness(t, NewBucketRequestPaymentResource(), client)

		resp := h.read(&bucketRequestPaymentModel{
			Bucket: types.StringValue("tf-test"),
			Payer:  types.StringValue("BucketOwner"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("payer"), "Requester")
	})

	t.Run("read removes a vanished bucket", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet,
			status: http.StatusNotFound,
			body:   s3ErrorXML("NoSuchBucket", "gone"),
		})
		h := newCRUDHarness(t, NewBucketRequestPaymentResource(), client)

		resp := h.read(&bucketRequestPaymentModel{Bucket: types.StringValue("tf-test")})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("update writes the new payer", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusOK})
		h := newCRUDHarness(t, NewBucketRequestPaymentResource(), client)

		resp := h.update(
			&bucketRequestPaymentModel{Bucket: types.StringValue("tf-test"), Payer: types.StringValue("BucketOwner")},
			&bucketRequestPaymentModel{Bucket: types.StringValue("tf-test"), Payer: types.StringValue("Requester")},
		)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("payer"), "BucketOwner")
		recorder.assertRequest(t, "PUT")
	})

	t.Run("read reports a server error", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no read"),
		})
		h := newCRUDHarness(t, NewBucketRequestPaymentResource(), client)

		resp := h.read(&bucketRequestPaymentModel{Bucket: types.StringValue("tf-test")})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading bucket request payment configuration", "no read")
	})

	t.Run("update failure is reported", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no update"),
		})
		h := newCRUDHarness(t, NewBucketRequestPaymentResource(), client)

		resp := h.update(
			&bucketRequestPaymentModel{Bucket: types.StringValue("tf-test"), Payer: types.StringValue("BucketOwner")},
			&bucketRequestPaymentModel{Bucket: types.StringValue("tf-test"), Payer: types.StringValue("Requester")},
		)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket request payment configuration", "no update")
	})

	t.Run("delete reports a server error", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no restore"),
		})
		h := newCRUDHarness(t, NewBucketRequestPaymentResource(), client)

		resp := h.delete(&bucketRequestPaymentModel{
			Bucket: types.StringValue("tf-test"),
			Payer:  types.StringValue("Requester"),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error deleting bucket request payment configuration", "no restore")
	})

	t.Run("delete restores the owner payer and tolerates a vanished bucket", func(t *testing.T) {
		for name, status := range map[string]int{
			"server accepts the restore": http.StatusOK,
			"bucket already gone":        http.StatusNotFound,
		} {
			t.Run(name, func(t *testing.T) {
				client, _ := routedClient(t, requestRoute{
					method: http.MethodPut,
					status: status,
					body:   s3ErrorXML("NoSuchBucket", "gone"),
				})
				h := newCRUDHarness(t, NewBucketRequestPaymentResource(), client)

				resp := h.delete(&bucketRequestPaymentModel{
					Bucket: types.StringValue("tf-test"),
					Payer:  types.StringValue("Requester"),
				})

				requireNoDiagnostics(t, resp.Diagnostics)
			})
		}
	})
}

func TestAccBucketRequestPayment_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-requester-pays-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_request_payment.test"

	tfresource.ParallelTest(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []tfresource.TestStep{
			{
				Config: testAccBucketRequestPaymentConfig(name, "Requester"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "payer", "Requester"),
					testAccCheckBucketStillExists(name),
				),
			},
			{
				// In place, and the follow-up plan must be empty.
				Config: testAccBucketRequestPaymentConfig(name, "BucketOwner"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "payer", "BucketOwner"),
					testAccCheckBucketStillExists(name),
				),
			},
			{
				Config:   testAccBucketRequestPaymentConfig(name, "BucketOwner"),
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

func testAccBucketRequestPaymentConfig(name, payer string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_request_payment" "test" {
  bucket = rustfs_bucket.test.name
  payer  = "%s"
}
`, name, payer)
}

func TestBucketRequestPaymentResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewBucketRequestPaymentResource().(*bucketRequestPaymentResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewBucketRequestPaymentResource().(*bucketRequestPaymentResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Resource Configure Type")
	})
}
