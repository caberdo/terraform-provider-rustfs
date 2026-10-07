## 0.1.0 (Unreleased)

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
- Admin API access is AWS SigV4-signed; S3 operations use `minio-go`
- Every resource supports import
- Enum attributes are validated with `stringvalidator.OneOf`
- Unit tests with `httptest`-mocked admin API responses plus `TF_ACC=1` acceptance tests against a
  containerised RustFS instance
- Documentation generated with `terraform-plugin-docs` from the schema and `examples/`
