# Terraform Provider for RustFS

[![Go Version](https://img.shields.io/github/go-mod/go-version/caberdo/terraform-provider-rustfs)](https://golang.org/doc/devel/release.html)
[![CI](https://img.shields.io/github/actions/workflow/status/caberdo/terraform-provider-rustfs/test.yml?branch=main)](https://github.com/caberdo/terraform-provider-rustfs/actions)

Terraform provider for managing [RustFS](https://github.com/rustfs/rustfs) — an S3-compatible object storage system. Manage buckets, IAM users, policies, service accounts, quotas, lifecycle rules, encryption, versioning, and more.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://golang.org/doc/install) >= 1.25 (to build from source)

## Support and compatibility

The provider talks to both the S3 API and the RustFS admin API (`/rustfs/admin/v3`). Features
that depend on server-side sub-systems — KMS, LDAP, storage tiers and ILM, audit and
notification targets — work once the matching RustFS sub-system is enabled and configured. See
[`docs/index.md`](./docs/index.md) for details.

## Resources

| Resource | Description |
|----------|-------------|
| `rustfs_bucket` | S3-compatible bucket management |
| `rustfs_bucket_encryption` | Server-side encryption (SSE-S3, SSE-KMS) |
| `rustfs_bucket_lifecycle_configuration` | Object lifecycle rules |
| `rustfs_bucket_notification` | Event notification queues |
| `rustfs_bucket_object_lock` | Object lock and retention |
| `rustfs_bucket_replication` | Cross-bucket replication |
| `rustfs_bucket_versioning` | Versioning configuration |
| `rustfs_group` | IAM group management with members |
| `rustfs_iam_backup_import` | Import IAM entities from backup |
| `rustfs_policy` | S3 policy management |
| `rustfs_quota` | Bucket quota limits |
| `rustfs_rebalance` | Trigger pool rebalancing |
| `rustfs_serviceaccount` | Service accounts / API keys |
| `rustfs_tier` | Storage tier management (S3, Azure, GCS, etc.) |
| `rustfs_user` | IAM user management |

## Data Sources

| Data Source | Description |
|-------------|-------------|
| `rustfs_bucket_metadata_backup` | Export bucket metadata as ZIP |
| `rustfs_iam_backup` | Export IAM entities as ZIP |
| `rustfs_pools` | List storage pools |
| `rustfs_users` | List IAM users |

## Example Usage

```terraform
terraform {
  required_providers {
    rustfs = {
      source  = "caberdo/rustfs"
      version = "~> 0.0.7"
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
  name = "my-bucket"
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
  bucket = rustfs_bucket.example.name
  mode   = "COMPLIANCE"
  days   = 365
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
