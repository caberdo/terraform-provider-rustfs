# Terraform Provider for RustFS

[![Go Version](https://img.shields.io/github/go-mod/go-version/caberdo/terraform-provider-rustfs)](https://golang.org/doc/devel/release.html)
[![CI](https://img.shields.io/github/actions/workflow/status/caberdo/terraform-provider-rustfs/test.yml?branch=main)](https://github.com/caberdo/terraform-provider-rustfs/actions)

Terraform provider for managing [RustFS](https://github.com/rustfs/rustfs) — an S3-compatible object storage system. Manage buckets, IAM users, policies, service accounts, quotas, lifecycle rules, encryption, versioning, and more.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://golang.org/doc/install) >= 1.27 (to build from source)

## Support and compatibility

The provider talks to both the S3 API and the RustFS admin API (`/rustfs/admin/v3`). Features
that depend on server-side sub-systems — KMS, LDAP, storage tiers and ILM, audit and
notification targets — work once the matching RustFS sub-system is enabled and configured. See
[`docs/index.md`](./docs/index.md) for details.

## Resources

| Resource | Description |
|----------|-------------|
| `rustfs_user` | IAM users |
| `rustfs_policy` | S3/IAM policies |
| `rustfs_serviceaccount` | Service accounts / API keys |
| `rustfs_ldap_service_account` | Service accounts scoped to an LDAP user |
| `rustfs_bucket` | S3 buckets |
| `rustfs_quota` | Per-bucket quota limits |
| `rustfs_user_policy_attachment` | Canned policy on an IAM user |
| `rustfs_group_policy_attachment` | Canned policy on an IAM group |
| `rustfs_ldap_policy_attachment` | Canned policy on an LDAP user/group |
| `rustfs_bucket_policy` | Raw S3 bucket policy document |
| `rustfs_bucket_public_access_block` | Block-public-access configuration |
| `rustfs_bucket_tags` | Bucket tags |
| `rustfs_bucket_cors` | Bucket CORS rules |
| `rustfs_bucket_versioning` | Bucket versioning |
| `rustfs_bucket_object_lock` | Object lock / retention |
| `rustfs_bucket_encryption` | SSE-S3 / SSE-KMS encryption |
| `rustfs_bucket_lifecycle_configuration` | Lifecycle rules |
| `rustfs_bucket_notification` | Event notification targets |
| `rustfs_bucket_replication` | Cross-bucket replication |
| `rustfs_bucket_durability` | Per-bucket durability override |
| `rustfs_site_replication` | Site-replication peers |
| `rustfs_remote_target` | Remote replication/notification targets |
| `rustfs_rebalance` | Trigger pool rebalancing |
| `rustfs_group` | IAM groups with members |
| `rustfs_tier` | Storage tiers (S3, Azure, GCS, etc.) |
| `rustfs_kms_key` | KMS master keys |
| `rustfs_config` | Server sub-system configuration (config-kv) |
| `rustfs_module_switch` | Feature module switches |
| `rustfs_audit_target` | Audit-log webhook/HTTP targets |
| `rustfs_iam_backup_import` | Import IAM entities from a backup |
| `rustfs_bucket_metadata_backup_import` | Import bucket metadata from a backup |

> **Note:** `rustfs_iam_backup_import`, `rustfs_bucket_metadata_backup_import` and `rustfs_rebalance`
> are action-style resources and deliberately do not support import.

## Data Sources

| Data Source | Description |
|-------------|-------------|
| `rustfs_pools` | Storage pools |
| `rustfs_users` | IAM users |
| `rustfs_groups` | IAM groups |
| `rustfs_user_mfa` | MFA status of a user |
| `rustfs_iam_backup` | IAM export (ZIP) |
| `rustfs_iam_policies` | Canned IAM policies |
| `rustfs_iam_policy` | A single canned IAM policy |
| `rustfs_quota` | Bucket quota |
| `rustfs_bucket_metadata_backup` | Bucket metadata export (ZIP) |
| `rustfs_metrics` | Metrics stream |
| `rustfs_health_info` | Cluster health / OBD diagnostics |
| `rustfs_storage_info` | Cluster storage info |
| `rustfs_server_info` | Cluster, server, pool and drive info |
| `rustfs_replication_metrics` | Replication transfer metrics |
| `rustfs_ilm_tier_stats` | Per-tier ILM statistics |
| `rustfs_kms_config` | KMS backend configuration |
| `rustfs_kms_status` | KMS service status |

## Example Usage

```terraform
terraform {
  required_providers {
    rustfs = {
      source  = "caberdo/rustfs"
      version = "~> 0.2.0"
    }
  }
}

# Provider configuration via environment variables
provider "rustfs" {}

# Or via provider block
provider "rustfs" {
  endpoint   = "127.0.0.1:9001"
  access_key = "admin"
  secret_key = "secret"
}

# Bucket
resource "rustfs_bucket" "example" {
  name                           = "my-bucket"
  object_lock_enabled_for_bucket = true
}

# User with access key
resource "rustfs_user" "example" {
  access_key = "myuser"
  secret_key = "supersecret"
  name       = "My User"
  status     = "enabled"
}

# Service account
resource "rustfs_serviceaccount" "ci_token" {
  access_key  = "ci-bot"
  secret_key  = "s3cret"
  name        = "CI Pipeline"
  description = "Token for CI/CD access"
}

# IAM policy
resource "rustfs_policy" "readwrite" {
  name = "readwrite"
  statement = [{
    effect    = "Allow"
    action    = ["s3:GetObject", "s3:PutObject", "s3:ListBucket"]
    resource = ["arn:aws:s3:::my-bucket", "arn:aws:s3:::my-bucket/*"]
  }]
}

# Bucket quota (10 GiB)
resource "rustfs_quota" "example" {
  bucket = rustfs_bucket.example.name
  quota  = 10737418240
}

# Versioning
resource "rustfs_bucket_versioning" "example" {
  bucket = rustfs_bucket.example.name
  status = "Enabled"
}

# Object lock
resource "rustfs_bucket_object_lock" "example" {
  bucket              = rustfs_bucket.example.name
  object_lock_enabled = "Enabled"

  rule = {
    default_retention = {
      mode = "COMPLIANCE"
      days = 365
    }
  }
}
```

More examples in the [`examples/`](./examples/) directory.

## Authentication

Credentials can be provided via the provider block or environment variables. Environment variables take precedence when both are set.

| Provider Attribute | Environment Variable | Description |
|--------------------|---------------------|-------------|
| `endpoint` | `RUSTFS_ENDPOINT` | RustFS server in `host:port` format |
| `access_key` | `RUSTFS_USER` | Access key / username |
| `secret_key` | `RUSTFS_SECRET` | Secret key / password |

> **Note:** `access_secret` is deprecated in favor of `secret_key`. Both are currently supported; `access_secret` will be removed in a future release. When both are set, `secret_key` takes precedence.

## Building

```bash
git clone https://github.com/caberdo/terraform-provider-rustfs.git
cd terraform-provider-rustfs
go build -o terraform-provider-rustfs
```

For local development, add to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "caberdo/rustfs" = "/path/to/terraform-provider-rustfs"
  }
}
```

## Testing

### Unit tests

```bash
go test ./pkg/rustfs/... -v
go test ./provider/... -v -skip '^TestAcc'  # Skip acceptance tests
```

### Acceptance tests

Requires a running RustFS instance:

```bash
# Start RustFS
podman-compose -f acc_test/docker-compose.yml up -d

# Run acceptance tests
RUSTFS_ENDPOINT="127.0.0.1:9001" \
RUSTFS_USER="rustfsadmin" \
RUSTFS_SECRET="rustfsadmin" \
TF_ACC=1 go test -v ./provider -run "TestAcc"

# Cleanup
podman-compose -f acc_test/docker-compose.yml down
```

## Documentation

Full resource documentation is available in the [`docs/`](./docs/) directory or on the [Terraform Registry](https://registry.terraform.io/providers/caberdo/rustfs).

Regenerate it from the schema and examples with:

```bash
make generate
```

Check that every example still matches the provider schema with:

```bash
make validate-examples
```

## License

[MPL-2.0](LICENSE)
