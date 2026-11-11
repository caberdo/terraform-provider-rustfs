// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccBucketResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-bucket-%d", acctest.RandInt())
	replacementName := fmt.Sprintf("tf-test-bucket-%d", acctest.RandInt())
	resourceName := "rustfs_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", name),
				),
			},
			{
				// Explicit computed consistency: the post-apply plan for the
				// configuration just applied must be empty.
				Config:             testAccBucketConfig(name),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Replacement plan: name carries RequiresReplace(), so a new
				// name must plan a replacement rather than an in-place update.
				Config:             testAccBucketConfig(replacementName),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement so the replacement path really runs and
				// the substitute bucket is created on the server.
				Config: testAccBucketConfig(replacementName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", replacementName),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        replacementName,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
	})
}

// TestAccBucketResource_aclInPlace proves a canned-ACL change updates the
// bucket instead of replacing it: the plan action is Update and the same bucket
// (and its objects) survives.
func TestAccBucketResource_aclInPlace(t *testing.T) {
	name := fmt.Sprintf("tf-test-bucket-acl-%d", acctest.RandInt())
	resourceName := "rustfs_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketACLConfig(name, "private"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "acl", "private"),
				),
			},
			{
				Config: testAccBucketACLConfig(name, "public-read"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "acl", "public-read"),
				),
			},
			{
				// The ACL is applied, not read back, so the follow-up plan is
				// empty rather than a permanent diff.
				Config:   testAccBucketACLConfig(name, "public-read"),
				PlanOnly: true,
			},
		},
	})
}

func testAccBucketACLConfig(name, acl string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
  acl  = "%s"
}
`, name, acl)
}

func testAccBucketConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}
`, name)
}

// TestAccBucketResource_forceDestroy proves the destructive switch empties the
// bucket in one destroy: two object versions and an in-flight multipart upload
// are left behind on purpose. The default (force_destroy = false) refusal is
// covered by TestBucketResourceForceDestroy, where a 409 BucketNotEmpty is
// asserted to surface as a diagnostic; the testing framework cannot assert a
// failing destroy without leaving a non-empty bucket behind for the final
// cleanup.
func TestAccBucketResource_forceDestroy(t *testing.T) {
	name := fmt.Sprintf("tf-test-force-destroy-%d", acctest.RandInt())
	resourceName := "rustfs_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketForceDestroyConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "force_destroy", "true"),
					testAccBucketSeedForceDestroy(name),
				),
			},
		},
	})
}

func testAccBucketForceDestroyConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name          = "%s"
  force_destroy = true
}

resource "rustfs_bucket_versioning" "test" {
  bucket = rustfs_bucket.test.name
  status = "Enabled"
}
`, name)
}

// testAccBucketSeedForceDestroy leaves the two obstacles a versioned bucket
// accumulates: several object versions (so a delete marker is created by the
// object pass) and an unfinished multipart upload.
func testAccBucketSeedForceDestroy(bucket string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return err
		}
		ctx := context.Background()

		for _, body := range []string{"first", "second"} {
			if _, err := client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String("versioned.txt"),
				Body:   strings.NewReader(body),
			}); err != nil {
				return fmt.Errorf("seeding object version: %w", err)
			}
		}

		upload, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("incomplete.bin"),
		})
		if err != nil {
			return fmt.Errorf("starting multipart upload: %w", err)
		}
		if _, err := client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String("incomplete.bin"),
			UploadId:   upload.UploadId,
			PartNumber: aws.Int32(1),
			Body:       strings.NewReader("payload"),
		}); err != nil {
			return fmt.Errorf("uploading part: %w", err)
		}
		return nil
	}
}

func testAccCheckBucketExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}

		bucketName := rs.Primary.Attributes["name"]
		if bucketName == "" {
			return fmt.Errorf("no bucket name set")
		}

		client, err := testAccS3Client()
		if err != nil {
			return err
		}

		exists, err := bucketExists(context.Background(), client, bucketName)
		if err != nil {
			return fmt.Errorf("error checking bucket: %s", err)
		}
		if !exists {
			return fmt.Errorf("bucket %s does not exist", bucketName)
		}
		return nil
	}
}

func testAccCheckBucketDestroy(s *terraform.State) error {
	client, err := testAccS3Client()
	if err != nil {
		return err
	}

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_bucket" {
			continue
		}

		bucketName := rs.Primary.Attributes["name"]
		if bucketName == "" {
			continue
		}

		exists, err := bucketExists(context.Background(), client, bucketName)
		if err != nil {
			return fmt.Errorf("error checking bucket destruction: %s", err)
		}
		if exists {
			return fmt.Errorf("bucket %s still exists", bucketName)
		}
	}
	return nil
}

func testAccS3Client() (*s3.Client, error) {
	return newTestS3Client(os.Getenv("RUSTFS_ENDPOINT"), os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"))
}

func newTestS3Client(endpoint, accessKey, secretKey string) (*s3.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	endpointURL := endpoint
	if !strings.Contains(endpointURL, "://") {
		endpointURL = "http://" + endpointURL
	}

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
		o.UsePathStyle = true
	}), nil
}

// TestBucketResourceLifecyclePaths covers the paths the acceptance suite cannot
// reach with a live server: an object that disappeared behind Terraform's back,
// a delete that arrives after the object is already gone, and every failing API
// call in Create, Read and Delete.
func TestBucketResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "not found status removes the resource from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading bucket",
			wantDetail:  "HeadBucket",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketResource(), client)

			resp := h.read(&bucketResourceModel{Name: types.StringValue("tf-test")})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting bucket",
			wantDetail:  "DeleteBucket",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketResource(), client)

			resp := h.delete(&bucketResourceModel{Name: types.StringValue("tf-test")})

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
		wantSummary string
		wantDetail  string
	}{
		{
			name: "rejected payload reports the server error and writes no state",
			routes: []requestRoute{
				{method: http.MethodHead, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")},
				{method: http.MethodPut, status: http.StatusConflict, body: s3ErrorXML("BucketAlreadyExists", "create exploded")},
			},
			wantSummary: "Error creating bucket",
			wantDetail:  "create exploded",
		},
		{
			name:        "existing bucket is refused",
			routes:      []requestRoute{{method: http.MethodHead, status: http.StatusOK}},
			wantSummary: "Error creating bucket",
			wantDetail:  "Already existing",
		},
		{
			name:        "failed existence check is reported",
			routes:      []requestRoute{{method: http.MethodHead, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error checking bucket",
			wantDetail:  "HeadBucket",
		},
	}

	t.Run("create: a fully populated plan is sent", func(t *testing.T) {
		client, recorder := routedClient(t,
			requestRoute{method: http.MethodHead, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")},
			requestRoute{method: http.MethodGet, pathContains: "/admin/v3/info", status: http.StatusOK, body: serverInfoBody("eu-west-1")},
			requestRoute{method: http.MethodPut, status: http.StatusOK},
			requestRoute{method: http.MethodGet, status: http.StatusOK},
		)
		h := newCRUDHarness(t, NewBucketResource(), client)
		plan := &bucketResourceModel{
			Name:                       types.StringValue("tf-test"),
			BucketNamespace:            types.StringValue("global"),
			ACL:                        types.StringValue("public-read"),
			ObjectOwnership:            types.StringValue("BucketOwnerEnforced"),
			ObjectLockEnabledForBucket: types.BoolValue(true),
			GrantRead:                  types.StringValue(`uri="http://acs.amazonaws.com/groups/global/AllUsers"`),
			GrantWrite:                 types.StringValue(`uri="http://acs.amazonaws.com/groups/global/AllUsers"`),
			GrantReadACP:               types.StringValue(`uri="http://acs.amazonaws.com/groups/global/AllUsers"`),
			GrantWriteACP:              types.StringValue(`uri="http://acs.amazonaws.com/groups/global/AllUsers"`),
			GrantFullControl:           types.StringValue("emailaddress=ops@example.com"),
			CreateBucketConfiguration: &createBucketConfigurationModel{
				LocationConstraint: types.StringValue("eu-west-1"),
				Tags:               types.MapNull(types.StringType),
			},
		}

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		recorder.assertRequest(t, "PUT")
	})

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketResource(), client)
			plan := &bucketResourceModel{Name: types.StringValue("tf-test")}

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

// TestBucketResourceACLUpdate covers the in-place ACL path: changing a canned
// ACL or a grant must issue PutBucketAcl instead of replacing the bucket, and
// Read must not mirror GetBucketAcl (RustFS reports only the owner grant, so a
// configured ACL would otherwise drift forever).
func TestBucketResourceACLUpdate(t *testing.T) {
	t.Parallel()

	type s3Call struct{ method, query string }

	handler := func(calls *[]s3Call, mu *sync.Mutex) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			*calls = append(*calls, s3Call{method: r.Method, query: r.URL.RawQuery})
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}
	}

	aclCalls := func(calls []s3Call) []s3Call {
		var out []s3Call
		for _, call := range calls {
			if call.method == http.MethodPut && strings.Contains(call.query, "acl") {
				out = append(out, call)
			}
		}
		return out
	}

	t.Run("a changed canned ACL is applied in place", func(t *testing.T) {
		var mu sync.Mutex
		var calls []s3Call
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, handler(&calls, &mu)))

		resp := h.update(
			&bucketResourceModel{Name: types.StringValue("tf-test"), ACL: types.StringValue("public-read")},
			&bucketResourceModel{Name: types.StringValue("tf-test"), ACL: types.StringValue("private")},
		)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("acl"), "public-read")
		if got := aclCalls(calls); len(got) != 1 {
			t.Errorf("expected exactly one PutBucketAcl, got %v", calls)
		}
		for _, call := range calls {
			if call.method == http.MethodPut && call.query == "" {
				t.Errorf("Update must not call CreateBucket, got %v", calls)
			}
		}
	})

	t.Run("a changed grant is applied in place", func(t *testing.T) {
		var mu sync.Mutex
		var calls []s3Call
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, handler(&calls, &mu)))

		resp := h.update(
			&bucketResourceModel{
				Name:      types.StringValue("tf-test"),
				GrantRead: types.StringValue(`uri="http://acs.amazonaws.com/groups/global/AllUsers"`),
			},
			&bucketResourceModel{Name: types.StringValue("tf-test")},
		)

		requireNoDiagnostics(t, resp.Diagnostics)
		if got := aclCalls(calls); len(got) != 1 {
			t.Errorf("expected exactly one PutBucketAcl, got %v", calls)
		}
	})

	t.Run("an ownership change does not touch the ACL", func(t *testing.T) {
		var mu sync.Mutex
		var calls []s3Call
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, handler(&calls, &mu)))

		resp := h.update(
			&bucketResourceModel{
				Name:            types.StringValue("tf-test"),
				ACL:             types.StringValue("private"),
				ObjectOwnership: types.StringValue("BucketOwnerEnforced"),
			},
			&bucketResourceModel{
				Name:            types.StringValue("tf-test"),
				ACL:             types.StringValue("private"),
				ObjectOwnership: types.StringValue("BucketOwnerPreferred"),
			},
		)

		requireNoDiagnostics(t, resp.Diagnostics)
		if got := aclCalls(calls); len(got) != 0 {
			t.Errorf("expected no PutBucketAcl, got %v", got)
		}
	})

	t.Run("read keeps the configured ACL instead of the owner-only answer", func(t *testing.T) {
		var mu sync.Mutex
		var calls []s3Call
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, handler(&calls, &mu)))

		resp := h.read(&bucketResourceModel{
			Name: types.StringValue("tf-test"),
			ACL:  types.StringValue("public-read"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("acl"), "public-read")
		for _, call := range calls {
			if call.method == http.MethodGet && strings.Contains(call.query, "acl") {
				t.Errorf("Read must not call GetBucketAcl, got %v", calls)
			}
		}
	})
}

func TestBuildBucketACLInput(t *testing.T) {
	plan := bucketResourceModel{
		Name:             types.StringValue("tf-test"),
		ACL:              types.StringValue("public-read"),
		GrantRead:        types.StringValue("read-grantee"),
		GrantWrite:       types.StringValue("write-grantee"),
		GrantReadACP:     types.StringValue("read-acp-grantee"),
		GrantWriteACP:    types.StringValue("write-acp-grantee"),
		GrantFullControl: types.StringValue("full-control-grantee"),
	}

	input := buildBucketACLInput(plan)
	if got := string(input.ACL); got != "public-read" {
		t.Errorf("acl = %q", got)
	}
	for name, pair := range map[string]struct {
		got  *string
		want string
	}{
		"grant_read":         {input.GrantRead, "read-grantee"},
		"grant_write":        {input.GrantWrite, "write-grantee"},
		"grant_read_acp":     {input.GrantReadACP, "read-acp-grantee"},
		"grant_write_acp":    {input.GrantWriteACP, "write-acp-grantee"},
		"grant_full_control": {input.GrantFullControl, "full-control-grantee"},
	} {
		if pair.got == nil || *pair.got != pair.want {
			t.Errorf("%s = %v, want %q", name, pair.got, pair.want)
		}
	}

	empty := buildBucketACLInput(bucketResourceModel{Name: types.StringValue("tf-test")})
	if empty.ACL != "" || empty.GrantRead != nil || empty.GrantWrite != nil ||
		empty.GrantReadACP != nil || empty.GrantWriteACP != nil || empty.GrantFullControl != nil {
		t.Errorf("unset attributes must not be sent: %+v", empty)
	}
}

// serverInfoBody renders the GET /admin/v3/info answer for region. An empty
// region is omitted, which is what a server that reports none sends.
func serverInfoBody(region string) string {
	if region == "" {
		return `{"info":{}}`
	}
	return fmt.Sprintf(`{"info":{"region":%q}}`, region)
}

// healthInfoBody renders the GET /admin/v3/healthinfo answer for region.
func healthInfoBody(region string) string {
	return fmt.Sprintf(`{"region":%q}`, region)
}

// TestBucketResourceLocationConstraint covers the create-time comparison of
// location_constraint with the region the server reports. RustFS 1.0.5 rejects a
// mismatch itself with InvalidLocationConstraint, so the provider refuses it
// first, before CreateBucket is sent, and names the accepted values.
func TestBucketResourceLocationConstraint(t *testing.T) {
	t.Parallel()

	createRoutes := func(regionRoutes ...requestRoute) []requestRoute {
		routes := []requestRoute{
			{method: http.MethodHead, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")},
		}
		routes = append(routes, regionRoutes...)
		return append(routes,
			requestRoute{method: http.MethodPut, status: http.StatusOK},
			requestRoute{method: http.MethodGet, status: http.StatusOK},
		)
	}
	infoRoute := func(body string) requestRoute {
		return requestRoute{
			method:       http.MethodGet,
			pathContains: "/admin/v3/info",
			status:       http.StatusOK,
			body:         body,
		}
	}
	plan := func(constraint string) *bucketResourceModel {
		return &bucketResourceModel{
			Name: types.StringValue("tf-test"),
			CreateBucketConfiguration: &createBucketConfigurationModel{
				LocationConstraint: types.StringValue(constraint),
				Tags:               types.MapNull(types.StringType),
			},
		}
	}
	countRequests := func(recorder *recordingServer, substr string) int {
		n := 0
		for _, req := range recorder.snapshot() {
			if strings.Contains(req, substr) {
				n++
			}
		}
		return n
	}

	t.Run("a matching region creates the bucket after one region read", func(t *testing.T) {
		client, recorder := routedClient(t, createRoutes(infoRoute(serverInfoBody("eu-west-1")))...)
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.create(plan("eu-west-1"))

		requireNoDiagnostics(t, resp.Diagnostics)
		if got := len(resp.Diagnostics); got != 0 {
			t.Errorf("expected no diagnostics, got %d: %v", got, resp.Diagnostics)
		}
		recorder.assertRequest(t, "PUT")
		if got := countRequests(recorder, "/admin/v3/info"); got != 1 {
			t.Errorf("expected one region read, got %d: %v", got, recorder.snapshot())
		}
	})

	t.Run("the EU alias for eu-west-1 creates the bucket", func(t *testing.T) {
		client, recorder := routedClient(t, createRoutes(infoRoute(serverInfoBody("eu-west-1")))...)
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.create(plan("EU"))

		requireNoDiagnostics(t, resp.Diagnostics)
		if got := len(resp.Diagnostics); got != 0 {
			t.Errorf("expected no diagnostics, got %d: %v", got, resp.Diagnostics)
		}
		recorder.assertRequest(t, "PUT")
	})

	t.Run("a mismatch fails before CreateBucket is sent", func(t *testing.T) {
		client, recorder := routedClient(t, createRoutes(infoRoute(serverInfoBody("eu-west-1")))...)
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.create(plan("us-east-1"))

		assertErrorDiagnostic(t, resp.Diagnostics,
			"Invalid location_constraint for bucket tf-test", `"eu-west-1"`)
		detail := resp.Diagnostics[0].Detail()
		for _, want := range []string{`"us-east-1"`, "EU", "omit create_bucket_configuration"} {
			if !strings.Contains(detail, want) {
				t.Errorf("diagnostic detail %q does not mention %q", detail, want)
			}
		}
		recorder.assertNoRequest(t, "PUT")
		h.assertNoPartialState(resp, plan("us-east-1"))
	})

	t.Run("EU is not accepted for any other region", func(t *testing.T) {
		client, recorder := routedClient(t, createRoutes(infoRoute(serverInfoBody("us-east-1")))...)
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.create(plan("EU"))

		assertErrorDiagnostic(t, resp.Diagnostics,
			"Invalid location_constraint for bucket tf-test", `"us-east-1"`)
		detail := resp.Diagnostics[0].Detail()
		if strings.Contains(detail, "or EU") {
			t.Errorf("detail offers the EU alias for a region that does not accept it: %q", detail)
		}
		recorder.assertNoRequest(t, "PUT")
	})

	t.Run("a server that reports no region skips the check", func(t *testing.T) {
		client, recorder := routedClient(t, createRoutes(infoRoute(serverInfoBody("")))...)
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.create(plan("us-east-1"))

		requireNoDiagnostics(t, resp.Diagnostics)
		recorder.assertRequest(t, "PUT")
	})

	t.Run("a failed info read falls back to healthinfo", func(t *testing.T) {
		client, recorder := routedClient(t, createRoutes(
			requestRoute{
				method:       http.MethodGet,
				pathContains: "/admin/v3/info",
				status:       http.StatusNotFound,
				body:         s3ErrorXML("NotFound", "no info route"),
			},
			requestRoute{
				method:       http.MethodGet,
				pathContains: "/admin/v3/healthinfo",
				status:       http.StatusOK,
				body:         healthInfoBody("eu-west-1"),
			},
		)...)
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.create(plan("eu-west-1"))

		requireNoDiagnostics(t, resp.Diagnostics)
		recorder.assertRequest(t, "/admin/v3/healthinfo")
		recorder.assertRequest(t, "PUT")
	})

	t.Run("an unreadable region warns and lets the server decide", func(t *testing.T) {
		client, recorder := routedClient(t, createRoutes(
			requestRoute{
				method:       http.MethodGet,
				pathContains: "/admin/v3/info",
				status:       http.StatusNotFound,
				body:         s3ErrorXML("NotFound", "no info route"),
			},
			requestRoute{
				method:       http.MethodGet,
				pathContains: "/admin/v3/healthinfo",
				status:       http.StatusNotFound,
				body:         s3ErrorXML("NotFound", "no health route"),
			},
		)...)
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.create(plan("us-east-1"))

		if resp.Diagnostics.HasError() {
			t.Fatalf("a failed region read must not fail the create: %v", resp.Diagnostics.Errors())
		}
		if got := resp.Diagnostics.WarningsCount(); got != 1 {
			t.Fatalf("expected one warning, got %d: %v", got, resp.Diagnostics)
		}
		if !strings.Contains(resp.Diagnostics[0].Summary(), "Could not read the RustFS region") {
			t.Errorf("warning summary = %q", resp.Diagnostics[0].Summary())
		}
		recorder.assertRequest(t, "PUT")
	})
}

// TestAccBucketResource_locationConstraint proves a constraint that matches the
// server's region is accepted live. The compose server runs RustFS' default
// region, so the region is read from the server instead of hardcoded; a server
// that reports none has nothing to match and the case skips. The mismatch case
// is covered by TestBucketResourceLocationConstraint: a failing apply is not
// expressible as a passing acceptance step.
func TestAccBucketResource_locationConstraint(t *testing.T) {
	region := testAccServerRegion(t)
	name := fmt.Sprintf("tf-test-bucket-location-%d", acctest.RandInt())
	resourceName := "rustfs_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketLocationConstraintConfig(name, region),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName,
						"create_bucket_configuration.location_constraint", region),
				),
			},
		},
	})
}

// testAccServerRegion returns the region the running RustFS reports. The probe
// runs before resource.ParallelTest, so it performs the acceptance precheck
// itself.
func testAccServerRegion(t *testing.T) string {
	t.Helper()
	testAccPreCheck(t)

	client := testAccRustClient()
	info, err := client.ServerInfo(context.Background())
	if err != nil {
		t.Fatalf("reading server info: %v", err)
	}
	if info.Info.Region == nil || *info.Info.Region == "" {
		t.Skip("server reports no region; there is no constraint to match")
	}
	return *info.Info.Region
}

func testAccBucketLocationConstraintConfig(name, region string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"

  create_bucket_configuration {
    location_constraint = "%s"
  }
}
`, name, region)
}

func TestBucketResourceUpdateFailures(t *testing.T) {
	t.Parallel()

	t.Run("an ACL write failure is reported", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no acl"),
		})
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.update(
			&bucketResourceModel{Name: types.StringValue("tf-test"), ACL: types.StringValue("public-read")},
			&bucketResourceModel{Name: types.StringValue("tf-test"), ACL: types.StringValue("private")},
		)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket ACL", "no acl")
	})

	t.Run("an ownership write failure is reported", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPut,
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "no ownership"),
		})
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.update(
			&bucketResourceModel{
				Name:            types.StringValue("tf-test"),
				ACL:             types.StringValue("private"),
				ObjectOwnership: types.StringValue("BucketOwnerEnforced"),
			},
			&bucketResourceModel{
				Name:            types.StringValue("tf-test"),
				ACL:             types.StringValue("private"),
				ObjectOwnership: types.StringValue("BucketOwnerPreferred"),
			},
		)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket object ownership", "no ownership")
	})
}
