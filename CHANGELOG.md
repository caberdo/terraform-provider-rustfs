## 0.3.0 (2026-10-08)

IMPROVEMENTS:
- Admin/S3 error responses are parsed into a structured `*APIError` (`Code`, `Message`,
  `StatusCode`) while `Error()` keeps returning the unchanged response body, so existing
  consumers keep working and not-found detection can use the structured codes
- Bucket not-found detection accepts the structured error codes in addition to the SDK error
  classes
- Acceptance tests now run against both RustFS `1.0.1` and `latest`

CHORES:
- Bumped CI actions (`hashicorp/setup-terraform` v4, `softprops/action-gh-release` v3,
  `anchore/sbom-action` v0.24.3)
- Completed the README resource and data source tables
- Removed the test-only admin bucket create/delete and `IsAdmin` helpers

## 0.2.0 (2026-10-08)

BREAKING CHANGES:
- `rustfs_bucket_encryption`: the flat `algorithm`/`kms_master_key_id` attributes are replaced by a
  `rule` list mirroring `ServerSideEncryptionRule`
- `rustfs_bucket_object_lock`: `mode`/`days`/`years` moved under `rule.default_retention`; new
  `object_lock_enabled` attribute
- `rustfs_bucket_replication`: the flat single-rule attributes are replaced by a `rule` list
  mirroring `ReplicationRule`; `delete_replication` is no longer accepted
- `rustfs_bucket_lifecycle_configuration`: `rule`, `transition` and `noncurrent_version_transition`
  are now (list) attributes instead of blocks; `filter` gained nested `tag`/`and`/object-size
  predicates
- Bucket sub-resources (`cors`, `tags`, `policy`, `public_access_block`, `lifecycle`) are now served
  by the AWS SDK S3 client instead of hand-rolled admin XML helpers

FEATURES:
- `rustfs_bucket` exposes the full `CreateBucketInput` (`acl`, `bucket_namespace`, `object_ownership`,
  `object_lock_enabled_for_bucket`, bucket grants and `create_bucket_configuration` with region and
  tags) and refreshes ownership, location and tags on read
- Bucket schemas now cover their AWS SDK structs: versioning (`mfa_delete`, `mfa`), encryption
  (`rule[]`, `bucket_key_enabled`), replication (full `rule[]` with filters, source-selection
  criteria and destination metrics/RTC/storage-class), notification
  (queue/topic/lambda/event-bridge targets)
- Multiple usage examples per resource, rendered verbatim into the registry documentation
- Enum validators are derived from the SDK enums

BUG FIXES:
- Idempotent reads and deletes for all bucket resources; not-found detection uses structured error
  codes instead of broad `404` substring matching
- `Optional+Computed` attributes are read back or null-normalized so refresh no longer drifts
- `rustfs_metrics` acceptance test skips when the server does not implement the admin metrics route
  (RustFS 1.0.1)
- Fixed the `rustfs_user_policy_attachment` acceptance test using `ressource` instead of `resource`

CHORES:
- Go toolchain bumped to 1.27.1; dependencies updated to latest (including `aws/smithy-go` v1.28.4)

## 0.1.0 (2026-10-07)

BREAKING CHANGES: None

FEATURES:
- **New Resource:** `rustfs_bucket` — S3-compatible bucket management
- **New Resource:** `rustfs_bucket_policy` — raw S3 bucket policy document
- **New Resource:** `rustfs_bucket_public_access_block` — block-public-access configuration
- **New Resource:** `rustfs_bucket_tags` — bucket tags
- **New Resource:** `rustfs_bucket_cors` — bucket CORS rules
- **New Resource:** `rustfs_bucket_versioning` — bucket versioning configuration
- **New Resource:** `rustfs_bucket_object_lock` — object lock and retention
- **New Resource:** `rustfs_bucket_encryption` — server-side encryption (SSE-S3, SSE-KMS)
- **New Resource:** `rustfs_bucket_lifecycle_configuration` — object lifecycle rules
- **New Resource:** `rustfs_bucket_notification` — event notification targets
- **New Resource:** `rustfs_bucket_replication` — cross-bucket replication
- **New Resource:** `rustfs_bucket_durability` — per-bucket durability override
- **New Resource:** `rustfs_bucket_metadata_backup_import` — import bucket metadata from a backup
- **New Resource:** `rustfs_site_replication` — site-replication peers
- **New Resource:** `rustfs_remote_target` — remote replication and notification targets
- **New Resource:** `rustfs_rebalance` — trigger pool rebalancing
- **New Resource:** `rustfs_quota` — per-bucket quota limits
- **New Resource:** `rustfs_user` — IAM user management
- **New Resource:** `rustfs_group` — IAM group management with members
- **New Resource:** `rustfs_policy` — S3/IAM policy management
- **New Resource:** `rustfs_user_policy_attachment` — attach a canned IAM policy to a user
- **New Resource:** `rustfs_group_policy_attachment` — attach a canned IAM policy to a group
- **New Resource:** `rustfs_ldap_policy_attachment` — attach a canned policy to an LDAP user or group
- **New Resource:** `rustfs_serviceaccount` — service accounts / API keys
- **New Resource:** `rustfs_ldap_service_account` — service account scoped to an LDAP user
- **New Resource:** `rustfs_iam_backup_import` — import IAM entities from a backup
- **New Resource:** `rustfs_tier` — storage tier management (S3, Azure, GCS, …)
- **New Resource:** `rustfs_kms_key` — KMS master keys
- **New Resource:** `rustfs_config` — server sub-system configuration (config-kv)
- **New Resource:** `rustfs_module_switch` — feature module switches
- **New Resource:** `rustfs_audit_target` — audit-log webhook/HTTP targets
- **New Data Source:** `rustfs_pools` — list storage pools
- **New Data Source:** `rustfs_users` — list IAM users
- **New Data Source:** `rustfs_groups` — list IAM groups
- **New Data Source:** `rustfs_user_mfa` — MFA status of a user
- **New Data Source:** `rustfs_iam_backup` — export IAM entities as ZIP
- **New Data Source:** `rustfs_iam_policies` — list canned IAM policies
- **New Data Source:** `rustfs_iam_policy` — inspect a single canned IAM policy
- **New Data Source:** `rustfs_quota` — read the quota of a bucket
- **New Data Source:** `rustfs_bucket_metadata_backup` — export bucket metadata as ZIP
- **New Data Source:** `rustfs_metrics` — metrics stream
- **New Data Source:** `rustfs_health_info` — cluster health and OBD diagnostics
- **New Data Source:** `rustfs_storage_info` — cluster storage info
- **New Data Source:** `rustfs_server_info` — cluster, server, pool and drive information
- **New Data Source:** `rustfs_replication_metrics` — replication transfer metrics
- **New Data Source:** `rustfs_ilm_tier_stats` — per-tier ILM storage statistics
- **New Data Source:** `rustfs_kms_config` — KMS backend configuration
- **New Data Source:** `rustfs_kms_status` — KMS service status
- Provider configuration via `endpoint`, `access_key` and `secret_key`, each overridable with
  the `RUSTFS_ENDPOINT`, `RUSTFS_USER` and `RUSTFS_SECRET` environment variables (the environment
  wins when both are set). `access_secret` is supported as a deprecated alias of `secret_key`.
- Optional TLS transport (`ssl`) and certificate-validation bypass (`insecure`)
- Admin API access is AWS SigV4-signed; S3 operations use the AWS SDK for Go v2
  (`github.com/aws/aws-sdk-go-v2/service/s3`)
- Every resource supports import
- Enum attributes are validated with `stringvalidator.OneOf`
- Unit tests with `httptest`-mocked admin API responses plus `TF_ACC=1` acceptance tests against a
  containerised RustFS instance
- Documentation generated with `terraform-plugin-docs` from the schema and `examples/`
