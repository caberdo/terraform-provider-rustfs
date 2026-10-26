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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const accelerateConfigurationPayload = `<?xml version="1.0" encoding="UTF-8"?>
<AccelerateConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Enabled</Status></AccelerateConfiguration>`

const accelerateConfigurationEmptyPayload = `<?xml version="1.0" encoding="UTF-8"?>
<AccelerateConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></AccelerateConfiguration>`

func TestBucketAccelerateResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewBucketAccelerateResource())
	for _, want := range []string{"bucket", "id", "status"} {
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

func TestBucketAccelerateResourceMetadata(t *testing.T) {
	resp := &resource.MetadataResponse{}
	NewBucketAccelerateResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_accelerate" {
		t.Errorf("expected rustfs_bucket_accelerate, got %s", resp.TypeName)
	}
}

func TestBucketAccelerateResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("create writes the status and records the id", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusOK})
		h := newCRUDHarness(t, NewBucketAccelerateResource(), client)
		plan := &bucketAccelerateModel{
			Bucket: types.StringValue("tf-test"),
			Status: types.StringValue("Enabled"),
		}

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "tf-test")
		recorder.assertRequest(t, "PUT")
	})

	t.Run("create failure reports the server error and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no accelerate for you"),
		})
		h := newCRUDHarness(t, NewBucketAccelerateResource(), client)
		plan := &bucketAccelerateModel{
			Bucket: types.StringValue("tf-test"),
			Status: types.StringValue("Enabled"),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating bucket accelerate configuration", "no accelerate for you")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("read mirrors the server status", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodGet, status: http.StatusOK, body: accelerateConfigurationPayload})
		h := newCRUDHarness(t, NewBucketAccelerateResource(), client)

		resp := h.read(&bucketAccelerateModel{
			Bucket: types.StringValue("tf-test"),
			Id:     types.StringValue("tf-test"),
			Status: types.StringValue("Suspended"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("status"), "Enabled")
	})

	t.Run("read removes a bucket whose configuration was never set", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodGet, status: http.StatusOK, body: accelerateConfigurationEmptyPayload})
		h := newCRUDHarness(t, NewBucketAccelerateResource(), client)

		resp := h.read(&bucketAccelerateModel{
			Bucket: types.StringValue("tf-test"),
			Status: types.StringValue("Enabled"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("read reports a server error and keeps state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "nope"),
		})
		h := newCRUDHarness(t, NewBucketAccelerateResource(), client)

		resp := h.read(&bucketAccelerateModel{Bucket: types.StringValue("tf-test")})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading bucket accelerate configuration", "nope")
		assertStatePresent(t, resp.State)
	})

	t.Run("update writes the new status", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusOK})
		h := newCRUDHarness(t, NewBucketAccelerateResource(), client)

		resp := h.update(
			&bucketAccelerateModel{Bucket: types.StringValue("tf-test"), Status: types.StringValue("Suspended")},
			&bucketAccelerateModel{Bucket: types.StringValue("tf-test"), Status: types.StringValue("Enabled")},
		)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("status"), "Suspended")
		recorder.assertRequest(t, "PUT")
	})

	t.Run("update failure is reported", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no update"),
		})
		h := newCRUDHarness(t, NewBucketAccelerateResource(), client)

		resp := h.update(
			&bucketAccelerateModel{Bucket: types.StringValue("tf-test"), Status: types.StringValue("Suspended")},
			&bucketAccelerateModel{Bucket: types.StringValue("tf-test"), Status: types.StringValue("Enabled")},
		)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket accelerate configuration", "no update")
	})

	t.Run("delete failure is reported", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no suspend"),
		})
		h := newCRUDHarness(t, NewBucketAccelerateResource(), client)

		resp := h.delete(&bucketAccelerateModel{
			Bucket: types.StringValue("tf-test"),
			Status: types.StringValue("Enabled"),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error deleting bucket accelerate configuration", "no suspend")
	})

	t.Run("delete suspends and tolerates a vanished bucket", func(t *testing.T) {
		for name, status := range map[string]int{
			"server accepts the suspend": http.StatusOK,
			"bucket already gone":        http.StatusNotFound,
		} {
			t.Run(name, func(t *testing.T) {
				client, _ := routedClient(t, requestRoute{
					method: http.MethodPut,
					status: status,
					body:   s3ErrorXML("NoSuchBucket", "gone"),
				})
				h := newCRUDHarness(t, NewBucketAccelerateResource(), client)

				resp := h.delete(&bucketAccelerateModel{
					Bucket: types.StringValue("tf-test"),
					Status: types.StringValue("Enabled"),
				})

				requireNoDiagnostics(t, resp.Diagnostics)
			})
		}
	})
}

func TestAccBucketAccelerate_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-accelerate-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_accelerate.test"

	tfresource.ParallelTest(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []tfresource.TestStep{
			{
				Config: testAccBucketAccelerateConfig(name, "Enabled"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "bucket", name),
					tfresource.TestCheckResourceAttr(resourceName, "id", name),
					tfresource.TestCheckResourceAttr(resourceName, "status", "Enabled"),
				),
			},
			{
				// In place, and the follow-up plan must be empty: the server
				// echoes what Put accepted.
				Config: testAccBucketAccelerateConfig(name, "Suspended"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "status", "Suspended"),
					testAccCheckBucketStillExists(name),
				),
			},
			{
				Config:   testAccBucketAccelerateConfig(name, "Suspended"),
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

func testAccBucketAccelerateConfig(name, status string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_accelerate" "test" {
  bucket = rustfs_bucket.test.name
  status = "%s"
}
`, name, status)
}

// testAccCheckBucketStillExists asserts an in-place update did not recreate the
// bucket: the name is the identity, so a replacement would have deleted it.
func testAccCheckBucketStillExists(name string) tfresource.TestCheckFunc {
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		exists, err := bucketExists(context.Background(), client, name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("bucket %s does not exist", name)
		}
		return nil
	}
}

func TestBucketAccelerateResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewBucketAccelerateResource().(*bucketAccelerateResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewBucketAccelerateResource().(*bucketAccelerateResource)
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Resource Configure Type")
	})
}

func TestBucketAccelerateResourceReadVanishedBucket(t *testing.T) {
	client, _ := routedClient(t, requestRoute{
		method: http.MethodGet,
		status: http.StatusNotFound,
		body:   s3ErrorXML("NoSuchBucket", "gone"),
	})
	h := newCRUDHarness(t, NewBucketAccelerateResource(), client)

	resp := h.read(&bucketAccelerateModel{
		Bucket: types.StringValue("tf-test"),
		Status: types.StringValue("Enabled"),
	})

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateRemoved(t, resp.State)
}
