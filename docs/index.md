---
page_title: "rustfs Provider"
subcategory: ""
description: |-
  Terraform provider for RustFS, S3-compatible object storage: buckets, IAM,
  encryption, replication, storage tiers, audit and notification targets, server
  configuration and cluster health.
---

# rustfs Provider

Terraform provider for [RustFS](https://github.com/rustfs/rustfs), an S3-compatible object storage
system. It talks to both the S3 API and the RustFS admin API v3 (`/rustfs/admin/v3`), so storage,
identity, encryption, replication, tiering and monitoring can live in the same Terraform state as
the rest of your infrastructure.

The complete, always-current list of supported resources and data sources is in the
[provider documentation](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs)
and the [README resource and data source indexes](https://github.com/caberdo/terraform-provider-rustfs#resources).

## Quick start

```terraform
terraform {
  required_providers {
    rustfs = {
      source  = "caberdo/rustfs"
      version = "~> 0.7"
    }
  }
}

provider "rustfs" {
  endpoint   = "127.0.0.1:9001"
  access_key = "rustfsadmin"
  secret_key = "rustfsadmin"
}

resource "rustfs_bucket" "artifacts" {
  name = "artifacts"
}

resource "rustfs_user" "ci" {
  access_key = "ci-bot"
  secret_key = "super-secret"
  name       = "CI pipeline"
  status     = "enabled"
}
```

The [README](https://github.com/caberdo/terraform-provider-rustfs#readme) documents every resource
in detail, including the complete resource and data source tables.

## Support and compatibility

- The provider speaks the S3 API through the AWS SDK for Go v2 S3 client and the RustFS admin
  API v3 (`/rustfs/admin/v3`), both with SigV4-signed requests.
- Terraform 1.0 or later is required (plugin protocol 6.0); the provider is also available for
  OpenTofu.
- CI exercises the acceptance suite against RustFS `1.0.1`, `1.0.2`, `1.0.3` and `latest`; see
  [`.github/rustfs-versions.txt`](https://github.com/caberdo/terraform-provider-rustfs/blob/main/.github/rustfs-versions.txt).
- Capabilities that depend on server-side sub-systems - KMS (`rustfs_kms_key`), LDAP
  (`rustfs_ldap_*`), storage tiers and ILM (`rustfs_tier`, `rustfs_rebalance`), audit
  targets (`rustfs_audit_target`) and notification targets - fail with the server's error
  until the matching RustFS sub-system is enabled and configured.
- Bucket encryption (`rustfs_bucket_encryption`) requires a KMS backend when using SSE-KMS;
  `AES256` (SSE-S3) works without one.

### Supported RustFS versions

| Capability | Provider types | `1.0.1` | `1.0.2` | `1.0.3` |
|---|---|:--:|:--:|:--:|
| S3 API and admin API v3 routes (buckets, IAM, quotas, replication, tiers, targets, config, health, info, …) | every resource and data source not listed below | ✅ | ✅ | ✅ |
| S3 Tables catalog switch — `PUT`/`GET /iceberg/v1/buckets/{bucket}` | `rustfs_table_bucket` create and read | ✅ | ✅ | ✅ |
| S3 Tables catalog disable — `DELETE /iceberg/v1/buckets/{bucket}` | `rustfs_table_bucket` destroy | ❌ | ✅ | ✅ |
| Admin metrics stream — `GET /rustfs/admin/v3/metrics` | `rustfs_metrics` data source | ❌ | ❌ | ❌ |
| OIDC providers — `GET`/`PUT`/`DELETE /rustfs/admin/v3/oidc/config` and `POST /rustfs/admin/v3/oidc/validate` | `rustfs_oidc_provider` resource, `rustfs_oidc_config` data source | ✅ | ✅ | ✅ |
| Orphaned bucket recovery — `POST /rustfs/admin/v3/recover-orphaned-bucket/{bucket}` | `rustfs_bucket_recovery` | ❌¹ | ✅ | ✅ |
| TLS certificate publication — `POST /rustfs/admin/v3/tls/certificate` | `rustfs_tls_certificate` create | ❌ | ✅ | ✅ |
| TLS certificate state — `GET /rustfs/admin/v3/tls/certificate-state` | `rustfs_tls_certificate` refresh, `rustfs_tls_certificate_state` data source | ❌ | ✅ | ✅ |

¹ The recovery route is absent on `1.0.1` (the server answers `404`), rather than registered and
unimplemented; the resource reports the route and the required `1.0.2` version.

`1.0.3` is a console-only update: it ships the same server code as `1.0.2`, so its server
capabilities are identical. A ❌ means the route is registered but not implemented (see below); the
data source fails with a diagnostic naming the route and the server version instead of the raw
error payload, and destroying `rustfs_table_bucket` on `1.0.1` warns, names the route and drops the
resource from state.

RustFS registers admin routes before it implements them and answers `501 NotImplemented` instead of
`404`, so a route in the server's route table is not proof of support — `DELETE
/iceberg/v1/buckets/{warehouse}` is registered but unimplemented on `1.0.1` (it works on `1.0.2` and
`1.0.3`). For the routes
it depends on, the provider treats `404`, `405` and `501` as a capability gap and reports the
feature, the route and the server version instead of the raw error payload.

### Non-goals

The provider models objects with a Terraform lifecycle. Some RustFS capabilities are deliberately
outside that model. Gaps that are merely **not built yet** are tracked in the
[issue tracker](https://github.com/caberdo/terraform-provider-rustfs/issues); the areas below are
**declined**, not pending.

| Area | Not modeled | Why it is out of scope |
|---|---|---|
| Diagnostics and one-shot actions | `top/locks`, `force-unlock`, `trace`, profiling/pprof, console `log`, `speedtest/*`, `inspect/archive`, `gateway-key-inventory`, `inspect-data`, `object-zip-downloads`, `POST /update` | They are imperative queries or operator actions with no durable desired state. `force-unlock` and update/restart mutate ephemeral server state that `plan`/`apply`/`destroy` cannot represent honestly; `top/locks`, profiling, tracing and speed tests report on demand. Use `mc` or the console. |
| Server-launch and environment-only features | WebDAV, FTP(S), SFTP, Swift, `RUSTFS_HTTP3_ENABLE`, scanner knobs exposed only as `RUSTFS_SCANNER_*` variables | They are enabled or tuned by the server process environment or launch flags, not through an admin API route. The provider talks to a running server over HTTP and does not control its process, container or command line; manage these with the deployment tooling that starts RustFS. The `scanner` subsystem of `rustfs_config` is still managed by `rustfs_config`; only knobs without a config-kv key are out of scope. |
| KMS service lifecycle | `POST /rustfs/admin/v3/kms/start`, `POST /rustfs/admin/v3/kms/stop`, `POST /rustfs/admin/v3/kms/reload`, `POST /rustfs/admin/v3/kms/configure`, `POST /rustfs/admin/v3/kms/reconfigure` | These control the KMS process or its configuration: start, stop and reload are process lifecycle, and configure/reconfigure edit backend configuration outside the declarative key model. The provider talks to a running server over HTTP; manage these with the deployment tooling that starts RustFS. `rustfs_kms_key`, `rustfs_kms_rekey`, `rustfs_kms_backup`, `rustfs_kms_config` and `rustfs_kms_status` cover the key, sweep, backup and read surfaces. |
| KMS one-shot and data-plane actions | `POST /rustfs/admin/v3/kms/clear-cache`, `POST /rustfs/admin/v3/kms/generate-data-key`, `POST /rustfs/admin/v3/kms/keys/cancel-deletion` | `clear-cache` is a one-shot performance mutation with no durable desired state; `generate-data-key` is a data-plane crypto operation, not infrastructure; `cancel-deletion` is a time-critical recovery action that must not become desired state — an apply could undo a deliberate key deletion. `rustfs_kms_key` guards the destructive path with `skip_destroy`; use `mc`, the console or the KMS backend's own tooling to recover a scheduled deletion. |
| v4 heal and MRF queue actions | `POST /rustfs/admin/v4/heal/mrf/responsibilities`, `POST /rustfs/admin/v4/heal/mrf/responsibilities/actions`, `/rustfs/admin/v4/heal/replacement-recovery` | They mutate the server's repair queue and replacement state, with no durable object Terraform can read back. `rustfs_replication_mrf` only reads the counters. Use `mc`, the console or the deployment tooling; whether heal becomes a provider type is tracked in the heal decision issue ([#158](https://github.com/caberdo/terraform-provider-rustfs/issues/158)). |
| Console self-service | `account/*`, `mfa/challenge` | These endpoints serve a signed-in human through browser sessions: account self-service and an interactive MFA challenge. They are not part of the SigV4 admin API and expose no declarative object for Terraform to own. The RustFS Console and the configured identity provider handle them. |
| Other products | RustFS Console UI, MCP server, operator/Helm charts | They are separate deliverables with their own repositories and release cadence. This provider is an API client; install, configure and upgrade those products with their own tooling. |
| Iceberg catalog data | Namespaces, tables, views, refs and credentials; catalog backup/restore | The catalog's contents are application data, not infrastructure. Catalog backup returns an opaque server-side snapshot with no read/list/delete route, so it has no Terraform lifecycle; restore is destructive and must not happen as a side effect of `apply`. Only the bucket-level switch is modeled, by `rustfs_table_bucket`. |

### Claiming a capability

A route in RustFS's `route_policy.rs` is **not** proof of support. RustFS registers routes before it
implements them and answers `501 NotImplemented` (see above). Before documenting or building a
capability, probe the running server, record the exact RustFS version and the response, and cite that
evidence in the issue or pull request. Do not infer support from the route table, a release note, or
a successful call on a different method or path.

## Migrating from `weinmann-emt/rustfs`

This provider is the successor of
[`weinmann-emt/terraform-provider-rustfs`](https://github.com/weinmann-emt/terraform-provider-rustfs).
Twelve of its sixteen resources and all six of its data sources are schema-compatible, so the move
is a state rewrite:

```bash
terraform state replace-provider weinmann-emt/rustfs caberdo/rustfs
terraform plan   # expect: No changes.
```

See the [migration table](https://github.com/caberdo/terraform-provider-rustfs#migrating-from-weinmann-emtrustfs)
for the three resources whose schemas differ and the one that was renamed.

## Verifying a release

Releases are built from GPG-signed, GitHub-verified tags and ship checksums signed with the
[release signing key](https://github.com/caberdo/terraform-provider-rustfs/blob/main/.github/release-signing-key.asc)
(fingerprint `77BEB69E5C039CB69017B060DFE36652ADCC2259`), SLSA build provenance for every
artifact, an SPDX SBOM and the registry manifest:

```bash
gpg --verify terraform-provider-rustfs_<version>_SHA256SUMS.sig \
             terraform-provider-rustfs_<version>_SHA256SUMS
gh attestation verify terraform-provider-rustfs_<version>_linux_amd64.zip \
    --repo caberdo/terraform-provider-rustfs
```

## Authentication

Credentials can be supplied through the provider block or through the environment.
Environment variables take precedence when both are set.

| Provider attribute | Environment variable | Description |
|--------------------|----------------------|-------------|
| `endpoint` | `RUSTFS_ENDPOINT` | RustFS server in `host:port` form |
| `access_key` | `RUSTFS_USER` | Access key / user name |
| `secret_key` | `RUSTFS_SECRET` | Secret key / password |

> **Note:** `access_secret` is deprecated in favour of `secret_key`. Both are supported;
> when both are set, `secret_key` wins.

## Example Usage

### Using provider attributes

```terraform
provider "rustfs" {
  endpoint   = "127.0.0.1:9001"
  access_key = "admin"
  secret_key = "secret"
}
```

### Using environment variables

```bash
export RUSTFS_ENDPOINT=127.0.0.1:9001
export RUSTFS_USER=admin
export RUSTFS_SECRET=secret
```

```terraform
provider "rustfs" {}
```

## Community

- [Issues and feature requests](https://github.com/caberdo/terraform-provider-rustfs/issues)
- [Changelog](https://github.com/caberdo/terraform-provider-rustfs/blob/main/CHANGELOG.md)
- [Security policy](https://github.com/caberdo/terraform-provider-rustfs/blob/main/.github/SECURITY.md)
- [Contributing](https://github.com/caberdo/terraform-provider-rustfs/blob/main/.github/CONTRIBUTING.md)

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `access_key` (String) Username or access key. Defaults to RUSTFS_USER environment variable.
- `access_secret` (String, Sensitive, Deprecated) Secret to be used as pass. Defaults to RUSTFS_SECRET environment variable.
- `endpoint` (String) RUSTFS server endpoint in the format host:port. Defaults to RUSTFS_ENDPOINT environment variable.
- `insecure` (Boolean) Insecure skip SSL validation
- `secret_key` (String, Sensitive) Secret key to be used as pass. Defaults to RUSTFS_SECRET environment variable.
- `ssl` (Boolean) Use SSL transport
