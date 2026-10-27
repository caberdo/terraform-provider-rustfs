// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// remoteTargetPeer returns the peer connection settings required to exercise
// rustfs_remote_target. Because set-remote-target validates that the remote
// endpoint is reachable and the destination bucket exists, a second running
// rustfs peer is required. When it is not configured the test is skipped.
type remoteTargetPeer struct {
	endpoint      string // peer S3 endpoint reachable from the rustfs server
	adminEndpoint string // peer admin endpoint reachable from the test host
	s3Endpoint    string // peer S3 endpoint reachable from the test host
	accessKey     string
	secretKey     string
}

func remoteTargetPeerConfig(t *testing.T) (*remoteTargetPeer, error) {
	t.Helper()
	p := &remoteTargetPeer{
		endpoint:      os.Getenv("RUSTFS_PEER_ENDPOINT"),
		adminEndpoint: os.Getenv("RUSTFS_PEER_ADMIN_ENDPOINT"),
		s3Endpoint:    os.Getenv("RUSTFS_PEER_S3_ENDPOINT"),
		accessKey:     os.Getenv("RUSTFS_PEER_ACCESS_KEY"),
		secretKey:     os.Getenv("RUSTFS_PEER_SECRET_KEY"),
	}
	// The three endpoints are distinct by design: the remote target's endpoint
	// must be the peer's S3 API as the primary server sees it, while the admin
	// and S3 endpoints the test host uses are reached through the published
	// ports. Falling back from one to another would silently point an S3 or
	// admin call at the wrong port, so every value is required.
	var missing []string
	for name, value := range map[string]string{
		"RUSTFS_PEER_ENDPOINT":       p.endpoint,
		"RUSTFS_PEER_ADMIN_ENDPOINT": p.adminEndpoint,
		"RUSTFS_PEER_S3_ENDPOINT":    p.s3Endpoint,
		"RUSTFS_PEER_ACCESS_KEY":     p.accessKey,
		"RUSTFS_PEER_SECRET_KEY":     p.secretKey,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("remote target acceptance test needs a peer: %s must be set", strings.Join(missing, ", "))
	}
	return p, nil
}

// testAccCheckRemoteTargetDestroy asserts that the remote target is gone from
// its source bucket. A bucket that no longer exists is an equally valid end
// state: the replication test lets Terraform destroy the source bucket.
func testAccCheckRemoteTargetDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_remote_target" {
			continue
		}
		bucket := rs.Primary.Attributes["bucket"]
		want := rs.Primary.Attributes["arn"]
		targets, err := client.ListRemoteTargets(context.Background(), bucket)
		if err != nil {
			if isBucketSubresourceAbsent(err) {
				continue
			}
			return err
		}
		for _, target := range targets {
			if target.Arn == want {
				return fmt.Errorf("remote target %s still exists on bucket %s", want, bucket)
			}
		}
	}
	return nil
}

// testAccCheckBucketAndRemoteTargetDestroy checks the buckets Terraform managed
// and the remote targets that referenced them.
func testAccCheckBucketAndRemoteTargetDestroy(s *terraform.State) error {
	if err := testAccCheckBucketDestroy(s); err != nil {
		return err
	}
	return testAccCheckRemoteTargetDestroy(s)
}

// testAccCheckRemoteTargetExists asserts that the target Terraform recorded is
// still registered on the server and that the server reports the credentials
// the state claims. The access key is read back from the admin API rather than
// from state, so an update the server silently dropped fails the check.
func testAccCheckRemoteTargetExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("not found: %s", resourceName)
		}
		bucket := rs.Primary.Attributes["bucket"]
		arn := rs.Primary.Attributes["arn"]
		if bucket == "" || arn == "" {
			return fmt.Errorf("remote target %s has no bucket/arn in state", resourceName)
		}
		client := testAccRustClient()
		targets, err := client.ListRemoteTargets(context.Background(), bucket)
		if err != nil {
			return err
		}
		for _, target := range targets {
			if target.Arn != arn {
				continue
			}
			if target.Credentials == nil {
				return fmt.Errorf("remote target %s has no credentials on the server", arn)
			}
			if want := rs.Primary.Attributes["access_key"]; target.Credentials.AccessKey != want {
				// The values are credentials: report the mismatch, not them.
				return fmt.Errorf("remote target %s reports an access key on the server that does not match the one in state", arn)
			}
			return nil
		}
		return fmt.Errorf("remote target %s not found on bucket %s", arn, bucket)
	}
}

// testAccMutateRemoteTargetOutOfBand removes the remote target straight through
// the admin API, behind Terraform's back, so the next refresh sees it missing
// and Terraform has to propose re-creating it.
func testAccMutateRemoteTargetOutOfBand(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("not found: %s", resourceName)
		}
		bucket := rs.Primary.Attributes["bucket"]
		arn := rs.Primary.Attributes["arn"]
		client := testAccRustClient()
		if err := client.DeleteRemoteTarget(context.Background(), bucket, arn); err != nil {
			return fmt.Errorf("removing remote target %s out of band: %w", arn, err)
		}
		targets, err := client.ListRemoteTargets(context.Background(), bucket)
		if err != nil {
			return fmt.Errorf("listing remote targets after out-of-band removal: %w", err)
		}
		for _, target := range targets {
			if target.Arn == arn {
				return fmt.Errorf("remote target %s is still registered after the out-of-band removal", arn)
			}
		}
		return nil
	}
}

// peerServiceAccount creates a service account on the peer with the caller's
// permissions, so the remote-target Update step can swap the target
// credentials for a second valid pair (set-remote-target validates them against
// the peer before persisting). The account is removed when the test ends.
func peerServiceAccount(t *testing.T, peer *remoteTargetPeer, accessKey, secretKey string) {
	t.Helper()
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    peer.accessKey,
		AccessSecret: peer.secretKey,
		Endpoint:     peer.adminEndpoint,
	})
	if err := client.CreateServiceAccount(context.Background(), rustfs.ServiceAccount{
		AccessKey:     accessKey,
		SecretKey:     secretKey,
		ImpliedPolicy: true,
	}); err != nil {
		t.Fatalf("creating peer service account %s: %v", accessKey, err)
	}
	t.Cleanup(func() {
		if err := client.DeleteServiceAccount(context.Background(), rustfs.ServiceAccount{AccessKey: accessKey}); err != nil {
			t.Logf("could not delete peer service account %s: %v", accessKey, err)
		}
	})
}

func TestRemoteTargetResourceSchema(t *testing.T) {
	r := NewRemoteTargetResource()
	resp := &fwresource.SchemaResponse{}
	r.Schema(context.Background(), fwresource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %s", resp.Diagnostics)
	}
	for _, attr := range []string{"arn", "type", "endpoint", "access_key", "secret_key", "secure", "region", "path", "bucket", "target_bucket"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("missing schema attribute %q", attr)
		}
	}

	// RustFS cannot change either value in place: the region is part of the ARN
	// and the path is only applied through an update group this client does not
	// send. Both must therefore force a replacement.
	for _, name := range []string{"region", "path"} {
		attr, ok := resp.Schema.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a StringAttribute", name)
		}
		if len(attr.PlanModifiers) != 1 {
			t.Fatalf("%s should carry exactly one plan modifier, got %d", name, len(attr.PlanModifiers))
		}
		requireReplacement(t, attr.PlanModifiers[0], name)
	}
}

func TestRemoteTargetResourceImportStateRejectsMalformedID(t *testing.T) {
	r, ok := NewRemoteTargetResource().(fwresource.ResourceWithImportState)
	if !ok {
		t.Fatal("remote target resource does not implement ResourceWithImportState")
	}
	resp := &fwresource.ImportStateResponse{}
	r.ImportState(context.Background(), fwresource.ImportStateRequest{ID: "missing-separator"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for an import ID without a bucket separator")
	}
}

func TestIsRemoteTargetNotFound(t *testing.T) {
	for _, msg := range []string{
		"Remote target not found for bucket: tf-bucket",
		"BucketRemoteTargetNotFound",
	} {
		if !isRemoteTargetNotFound(errors.New(msg)) {
			t.Errorf("expected %q to be recognised as not found", msg)
		}
	}
	if isRemoteTargetNotFound(errors.New("connection refused")) {
		t.Error("connection errors must not be treated as not found")
	}
}

func TestAccRemoteTargetResource(t *testing.T) {
	peer, err := remoteTargetPeerConfig(t)
	if err != nil {
		t.Skipf("skipping remote target acceptance test: %v", err)
	}

	// The source bucket must be versioned and the destination bucket must
	// exist on the (versioned) peer for set-remote-target to succeed.
	srcBucket := fmt.Sprintf("tf-rt-src-%d", acctest.RandInt())
	dstBucket := fmt.Sprintf("tf-rt-dst-%d", acctest.RandInt())

	srcClient, err := testAccS3Client()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srcClient.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(srcBucket)}); err != nil {
		t.Fatalf("creating source bucket: %v", err)
	}
	t.Cleanup(func() {
		_, _ = srcClient.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(srcBucket)})
	})
	if _, err := srcClient.PutBucketVersioning(context.Background(), &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(srcBucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("enabling versioning on source bucket: %v", err)
	}

	peerClient, err := newTestS3Client(peer.s3Endpoint, peer.accessKey, peer.secretKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peerClient.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(dstBucket)}); err != nil {
		t.Fatalf("creating destination bucket on peer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = peerClient.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(dstBucket)})
	})
	if _, err := peerClient.PutBucketVersioning(context.Background(), &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(dstBucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("enabling versioning on destination bucket: %v", err)
	}

	// A second valid credential pair on the peer lets the Update step exercise
	// the server's `creds` update group: set-remote-target re-validates the
	// credentials against the peer, so an arbitrary replacement would fail.
	saAccessKey := fmt.Sprintf("tf-rt-sa-%d", acctest.RandInt())
	// RustFS enforces an 8..40 character secret key for service accounts.
	saSecretKey := fmt.Sprintf("tf-rt-secret-%019d", acctest.RandInt())
	peerServiceAccount(t, peer, saAccessKey, saSecretKey)

	resourceName := "rustfs_remote_target.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckRemoteTargetDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRemoteTargetConfig(srcBucket, dstBucket, peer, "false", "us-east-1", peer.accessKey, peer.secretKey),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "type", "replication"),
					resource.TestCheckResourceAttr(resourceName, "bucket", srcBucket),
					resource.TestCheckResourceAttr(resourceName, "target_bucket", dstBucket),
					resource.TestCheckResourceAttr(resourceName, "region", "us-east-1"),
					resource.TestCheckResourceAttrSet(resourceName, "arn"),
				),
			},
			{
				ResourceName: resourceName,
				ImportState:  true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources[resourceName]
					if !ok {
						return "", fmt.Errorf("not found: %s", resourceName)
					}
					return rs.Primary.Attributes["bucket"] + ":" + rs.Primary.Attributes["arn"], nil
				},
				ImportStateVerifyIdentifierAttribute: "arn",
				ImportStateVerify:                    true,
				ImportStateVerifyIgnore:              []string{"secret_key"},
			},
			{
				// Import accepts the composite <source-bucket>:<arn> ID only.
				// An ID without the separator has to produce the explicit
				// diagnostic rather than a panic or a half-filled state.
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: "no-separator-here",
				ExpectError:   regexp.MustCompile(`(?s)Invalid import ID.*Expected import ID in the format <source-bucket>:<arn>`),
			},
			{
				// In-place update of the credentials group. Neither access_key
				// nor secret_key carries RequiresReplace, so the plan has to be
				// an update, and the server has to hand the new credential back.
				Config: testAccRemoteTargetConfig(srcBucket, dstBucket, peer, "false", "us-east-1", saAccessKey, saSecretKey),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "access_key", saAccessKey),
					resource.TestCheckResourceAttrSet(resourceName, "arn"),
					testAccCheckRemoteTargetExists(resourceName),
				),
			},
			{
				// Explicit empty second plan: the state the update above wrote
				// is the configuration, so neither the plan made from that state
				// nor the plan after a refresh may propose a change.
				Config:   testAccRemoteTargetConfig(srcBucket, dstBucket, peer, "false", "us-east-1", saAccessKey, saSecretKey),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// path carries RequiresReplace and the server never applies it
				// on update, so setting it has to replace the target. The step
				// only plans: the replacement is asserted, never applied.
				Config:             testAccRemoteTargetConfigWithPath(srcBucket, dstBucket, peer, "false", "us-east-1", "tf-rt-prefix", saAccessKey, saSecretKey),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Region is part of the target ARN and the server ignores it on
				// update, so a region change replaces the target.
				Config: testAccRemoteTargetConfig(srcBucket, dstBucket, peer, "false", "eu-west-1", saAccessKey, saSecretKey),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "region", "eu-west-1"),
					resource.TestCheckResourceAttrSet(resourceName, "arn"),
					testAccCheckRemoteTargetExists(resourceName),
				),
			},
			{
				// Out-of-band drift: the Check function removes the target
				// straight through the admin API after the (no-op) apply, so
				// the plan made before the refresh is still empty while the
				// plan Terraform proposes after the refresh has to re-create
				// the resource.
				Config:             testAccRemoteTargetConfig(srcBucket, dstBucket, peer, "false", "eu-west-1", saAccessKey, saSecretKey),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckRemoteTargetExists(resourceName),
					testAccMutateRemoteTargetOutOfBand(resourceName),
				),
			},
			{
				// Apply the drift the previous step discovered and assert the
				// re-created target is on the server again.
				Config: testAccRemoteTargetConfig(srcBucket, dstBucket, peer, "false", "eu-west-1", saAccessKey, saSecretKey),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "region", "eu-west-1"),
					resource.TestCheckResourceAttr(resourceName, "access_key", saAccessKey),
					testAccCheckRemoteTargetExists(resourceName),
				),
			},
		},
	})
}

func testAccRemoteTargetConfig(srcBucket, dstBucket string, peer *remoteTargetPeer, secure, region, accessKey, secretKey string) string {
	return testAccRemoteTargetConfigWithPath(srcBucket, dstBucket, peer, secure, region, "", accessKey, secretKey)
}

// testAccRemoteTargetConfigWithPath is testAccRemoteTargetConfig with the path
// attribute set. Only the plan-only replacement check uses it, so no target
// with a path is ever applied.
func testAccRemoteTargetConfigWithPath(srcBucket, dstBucket string, peer *remoteTargetPeer, secure, region, path, accessKey, secretKey string) string {
	pathAttribute := ""
	if path != "" {
		pathAttribute = fmt.Sprintf("  path          = %q\n", path)
	}
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_remote_target" "test" {
  type          = "replication"
  endpoint      = "%s"
  access_key    = "%s"
  secret_key    = "%s"
  secure        = %s
  region        = "%s"
  bucket        = "%s"
  target_bucket = "%s"
%s}
`, peer.endpoint, accessKey, secretKey, secure, region, srcBucket, dstBucket, pathAttribute)
}

var (
	lifecycleRemoteTargetAccessKey = unitTestCredential("RUSTFS_TEST_REMOTE_ACCESS_KEY", 'n')
	lifecycleRemoteTargetSecretKey = unitTestCredential("RUSTFS_TEST_REMOTE_SECRET_KEY", 'o')
)

// TestRemoteTargetResourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: a target that disappeared behind
// Terraform's back, a destroy that arrives after the target (or its whole
// source bucket) is already gone, and every failing API call in Create, Read
// and Delete.
//
// Live evidence (RustFS latest at 127.0.0.1:9301, probed 2026-10-19):
// list-remote-targets and remove-remote-target for a bucket that does not
// exist both answer 404 NoSuchBucket, so a missing source bucket is the
// provider's missing-target signal for this resource.
func TestRemoteTargetResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// The list endpoint answers 200 with an empty array for a bucket
			// that holds no remote target.
			name: "target absent from the list is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "list-remote-targets",
				status: http.StatusOK, body: `[]`,
			}},
			wantRemoved: true,
		},
		{
			// The source bucket is gone, so the target registered on it is
			// gone too: plan a recreation instead of failing the refresh.
			name: "missing bucket is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "list-remote-targets",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist"),
			}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error listing remote targets",
			wantDetail:  "AccessDenied",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewRemoteTargetResource(), client)

			resp := h.read(&RemoteTargetResourceModel{
				Bucket: types.StringValue("tf-test"),
				Arn:    types.StringValue("arn:rustfs:replication::tf-test"),
			})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	t.Run("read: listed target mirrors the server record", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "list-remote-targets", status: http.StatusOK,
			body: `[{"arn":"arn:rustfs:replication::tf-test","type":"replication","endpoint":"http://peer:9000","secure":false,"targetbucket":"dst-bucket","credentials":{"accessKey":"ak","secretKey":"sk"}}]`,
		})
		h := newCRUDHarness(t, NewRemoteTargetResource(), client)

		resp := h.read(&RemoteTargetResourceModel{
			Bucket: types.StringValue("tf-test"),
			Arn:    types.StringValue("arn:rustfs:replication::tf-test"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("endpoint"), "http://peer:9000")
		assertStateString(t, resp.State, path.Root("target_bucket"), "dst-bucket")
	})

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// The server accepts the delete of a target that is not there.
			name:   "already deleted target is not an error",
			routes: []requestRoute{{method: http.MethodDelete, pathContains: "remove-remote-target", status: http.StatusOK}},
		},
		{
			// The source bucket is gone (live evidence: 404 NoSuchBucket), so
			// the target cannot survive on it and the delete is already
			// satisfied.
			name: "missing bucket is not an error",
			routes: []requestRoute{{
				method: http.MethodDelete, pathContains: "remove-remote-target",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist"),
			}},
		},
		{
			name: "real server error is reported",
			routes: []requestRoute{{
				method: http.MethodDelete, pathContains: "remove-remote-target",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error deleting remote target",
			wantDetail:  "AccessDenied",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewRemoteTargetResource(), client)

			resp := h.delete(&RemoteTargetResourceModel{
				Bucket: types.StringValue("tf-test"),
				Arn:    types.StringValue("arn:rustfs:replication::tf-test"),
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
		plan        *RemoteTargetResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			// Live evidence: set-remote-target on a bucket that does not exist
			// answers 404 NoSuchBucket.
			name: "missing source bucket writes no state",
			routes: []requestRoute{{
				method: http.MethodPut, pathContains: "set-remote-target", status: http.StatusNotFound,
				body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist"),
			}},
			plan:        remoteTargetPlan("tf-test", "replication"),
			wantSummary: "Error creating remote target",
			wantDetail:  "The specified bucket does not exist",
		},
		{
			name: "unsupported target type writes no state",
			routes: []requestRoute{{
				method: http.MethodPut, pathContains: "set-remote-target", status: http.StatusBadRequest,
				body: s3ErrorXML("InvalidArgument", "invalid remote target type: 'bogus'"),
			}},
			plan:        remoteTargetPlan("tf-test", "bogus"),
			wantSummary: "Error creating remote target",
			wantDetail:  "invalid remote target type: 'bogus'",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewRemoteTargetResource(), client)

			resp := h.create(tc.plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
			rec.assertNoRequest(t, "list-remote-targets")
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewRemoteTargetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

// remoteTargetPlan builds a complete remote-target plan: endpoint, access_key,
// secret_key, bucket and target_bucket are required by the schema.
func remoteTargetPlan(bucket, targetType string) *RemoteTargetResourceModel {
	return &RemoteTargetResourceModel{
		Type:         types.StringValue(targetType),
		Endpoint:     types.StringValue("http://peer:9000"),
		AccessKey:    types.StringValue(lifecycleRemoteTargetAccessKey),
		SecretKey:    types.StringValue(lifecycleRemoteTargetSecretKey),
		Secure:       types.BoolValue(false),
		Bucket:       types.StringValue(bucket),
		TargetBucket: types.StringValue("dst-bucket"),
	}
}
