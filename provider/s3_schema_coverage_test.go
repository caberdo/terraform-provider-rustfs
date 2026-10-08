package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// TestS3ResourceSchemaCoverage guards the S3 schema expansion work: each
// bucket-related resource must keep exposing the full set of top-level
// attributes that map to its AWS SDK / RustFS API struct. If a field is
// intentionally dropped, update this table together with a note explaining why.
func TestS3ResourceSchemaCoverage(t *testing.T) {
	cases := []struct {
		name  string
		r     resource.Resource
		attrs []string
	}{
		{
			name: "rustfs_bucket",
			r:    NewBucketResource(),
			attrs: []string{
				"name", "acl", "bucket_namespace", "object_ownership",
				"object_lock_enabled_for_bucket", "grant_read", "grant_write",
				"grant_read_acp", "grant_write_acp", "grant_full_control",
				"create_bucket_configuration",
			},
		},
		{
			name:  "rustfs_bucket_versioning",
			r:     NewBucketVersioningResource(),
			attrs: []string{"bucket", "status", "mfa_delete", "mfa", "checksum_algorithm", "content_md5", "expected_bucket_owner"},
		},
		{
			name:  "rustfs_bucket_encryption",
			r:     NewBucketEncryptionResource(),
			attrs: []string{"bucket", "rule", "checksum_algorithm", "content_md5", "expected_bucket_owner"},
		},
		{
			name:  "rustfs_bucket_object_lock",
			r:     NewBucketObjectLockResource(),
			attrs: []string{"bucket", "object_lock_enabled", "rule", "checksum_algorithm", "content_md5", "expected_bucket_owner"},
		},
		{
			name:  "rustfs_bucket_replication",
			r:     NewBucketReplicationResource(),
			attrs: []string{"bucket", "role", "rule", "token", "checksum_algorithm", "content_md5", "expected_bucket_owner"},
		},
		{
			name:  "rustfs_bucket_notification",
			r:     NewBucketNotificationResource(),
			attrs: []string{"bucket", "queue", "topic", "lambda", "event_bridge", "skip_destination_validation", "expected_bucket_owner"},
		},
		{
			name:  "rustfs_bucket_lifecycle_configuration",
			r:     NewBucketLifecycleConfigurationResource(),
			attrs: []string{"bucket", "id", "rule", "transition_default_minimum_object_size"},
		},
		{
			name:  "rustfs_bucket_cors",
			r:     NewBucketCorsRessource(),
			attrs: []string{"bucket", "id", "rule"},
		},
		{
			name:  "rustfs_bucket_tags",
			r:     NewBucketTagsRessource(),
			attrs: []string{"bucket", "id", "tags"},
		},
		{
			name:  "rustfs_bucket_policy",
			r:     NewBucketPolicyRessource(),
			attrs: []string{"bucket", "id", "policy"},
		},
		{
			name:  "rustfs_bucket_public_access_block",
			r:     NewBucketPublicAccessBlockRessource(),
			attrs: []string{"bucket", "id", "block_public_acls", "ignore_public_acls", "block_public_policy", "restrict_public_buckets"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &resource.SchemaResponse{}
			tc.r.Schema(context.TODO(), resource.SchemaRequest{}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
			}

			got := map[string]bool{}
			for name := range resp.Schema.GetAttributes() {
				got[name] = true
			}
			for name := range resp.Schema.GetBlocks() {
				got[name] = true
			}

			for _, want := range tc.attrs {
				if !got[want] {
					t.Errorf("resource %s: missing expected schema attribute %q", tc.name, want)
				}
			}
		})
	}
}
