# Terraform Provider for RustFS

[![CI](https://img.shields.io/github/actions/workflow/status/caberdo/terraform-provider-rustfs/test.yml?branch=main&label=CI)](https://github.com/caberdo/terraform-provider-rustfs/actions/workflows/test.yml)
<!-- coverage-badge:start -->
[![Coverage](https://img.shields.io/badge/coverage-90.2%25-brightgreen)](#testing-and-coverage)
<!-- coverage-badge:end -->
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/caberdo/terraform-provider-rustfs/badge)](https://scorecard.dev/viewer/?uri=github.com/caberdo/terraform-provider-rustfs)
[![Release](https://img.shields.io/github/v/release/caberdo/terraform-provider-rustfs?label=release)](https://github.com/caberdo/terraform-provider-rustfs/releases)
[![Terraform Registry](https://img.shields.io/badge/registry-caberdo%2Frustfs-7b42bc)](https://registry.terraform.io/providers/caberdo/rustfs)
[![OpenTofu](https://img.shields.io/badge/OpenTofu-available-ffda18)](https://search.opentofu.org/provider/caberdo/rustfs)
[![Go](https://img.shields.io/github/go-mod/go-version/caberdo/terraform-provider-rustfs)](https://golang.org/doc/devel/release.html)
[![License](https://img.shields.io/github/license/caberdo/terraform-provider-rustfs)](LICENSE)
[![Last commit](https://img.shields.io/github/last-commit/caberdo/terraform-provider-rustfs)](https://github.com/caberdo/terraform-provider-rustfs/commits/main)

Manage **[RustFS](https://github.com/rustfs/rustfs)** — high-performance, S3-compatible object
storage — as code. This provider covers the S3 API **and** the RustFS admin API v3
(`/rustfs/admin/v3`), so buckets, IAM, encryption, replication, storage tiers, event targets and
cluster health all live in the same Terraform state as the rest of your infrastructure.

<!-- BEGIN AUTOGEN:counts -->
**48 resources and 37 data sources.**
<!-- END AUTOGEN:counts -->

| | |
|---|---|
| 🎯 **Broadest RustFS coverage** | Buckets, IAM (users, groups, policies, service accounts, LDAP), KMS keys, quotas, bucket + site replication, storage tiers and ILM, audit and notification targets, durability, module switches, cluster health, metrics and diagnostics |
| 🧪 **Tested against real servers** | Unit tests for every client method and resource lifecycle path (four fuzz targets replay a committed seed corpus), plus acceptance tests against RustFS in Docker on a version matrix (`1.0.1`, `1.0.2`, `1.0.3` and `latest`) |
| 🔐 **Verifiable supply chain** | GPG-signed release tags, signed `SHA256SUMS`, SLSA build provenance for every artifact, SPDX SBOM and the Terraform registry manifest in every release |
| 🚚 **Drop-in successor** | 12 of the 16 resources of `weinmann-emt/rustfs` migrate with **zero** config changes, 1 has a rename, 3 need a small edit — see [Migrating](#migrating-from-weinmann-emtrustfs) |
| ⚡ **Modern internals** | terraform-plugin-framework (plugin protocol 6.0), AWS SDK for Go v2, Go 1.27, Docker/Podman acceptance harness |

## Contents

- [Quick start](#quick-start)
- [Requirements and compatibility](#requirements-and-compatibility)
- [Non-goals](#non-goals)
- [Claiming a capability](#claiming-a-capability)
- [Provider configuration](#provider-configuration)
- [Resources](#resources)
- [Data sources](#data-sources)
- [Examples](#examples)
- [Migrating from weinmann-emt/rustfs](#migrating-from-weinmann-emtrustfs)
- [Verifying a release](#verifying-a-release)
- [Testing and coverage](#testing-and-coverage)
- [Development](#development)
- [Troubleshooting and FAQ](#troubleshooting-and-faq)
- [Security, contributing and support](#security-contributing-and-support)
- [License](#license)

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

# Credentials can come from the environment:
#   export RUSTFS_ENDPOINT=127.0.0.1:9001
#   export RUSTFS_USER=rustfsadmin
#   export RUSTFS_SECRET=rustfsadmin
provider "rustfs" {}

resource "rustfs_bucket" "artifacts" {
  name = "artifacts"
}

resource "rustfs_user" "ci" {
  access_key = "ci-bot"
  secret_key = "super-secret"
  name       = "CI pipeline"
  status     = "enabled"
}

resource "rustfs_quota" "artifacts" {
  bucket = rustfs_bucket.artifacts.name
  quota  = 10737418240 # 10 GiB
}
```

```bash
terraform init
terraform apply
```

Want a local server to try it against? The repository ships the same Docker Compose set-up that CI
uses for the acceptance suite:

```bash
docker compose -f acc_test/docker-compose.yml up -d rustfs   # API on :9000, admin API on :9001
terraform apply                                              # with RUSTFS_ENDPOINT=127.0.0.1:9001
```

## Requirements and compatibility

| | |
|---|---|
| Terraform | 1.0 or later (plugin protocol 6.0); also available for [OpenTofu](https://search.opentofu.org/provider/caberdo/rustfs) |
| RustFS | Acceptance-tested against `1.0.1`, `1.0.2`, `1.0.3` and `latest` — see the [version matrix](#supported-rustfs-versions) and [`.github/rustfs-versions.txt`](./.github/rustfs-versions.txt) |
| Go (to build from source) | 1.27.3 |

### Supported RustFS versions

CI runs the acceptance suite against RustFS `1.0.1`, `1.0.2`, `1.0.3` and `latest`
([`.github/rustfs-versions.txt`](./.github/rustfs-versions.txt)). The provider does not enforce a
server version up front; it reports a capability gap when a route it needs is unavailable.

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

#### Route presence is not support (501 semantics)

RustFS registers admin routes before it implements them and answers `501 NotImplemented` (an
S3-style `<Error><Code>NotImplemented</Code></Error>` body) instead of `404`. A route in the server's
route table is therefore not proof of support — `DELETE /iceberg/v1/buckets/{warehouse}` is
registered but unimplemented on `1.0.1` (it works on `1.0.2` and `1.0.3`). For the routes it depends
on, the provider treats `404`, `405` and `501` as a capability gap and reports an
`UnsupportedFeatureError` naming the feature, the route and the server version:

```text
Error: Metrics are not available on this RustFS server

  with data.rustfs_metrics.test,

RustFS 1.0.3 does not implement GET /rustfs/admin/v3/metrics: metrics is not available on this
server. Upgrade RustFS to a release that implements the route, or remove the rustfs_metrics data
source from the configuration.
```

`rustfs_tls_certificate` is different in two ways. RustFS accepts certificate management only on
its native TLS listener, so the provider needs `ssl = true` and a server started with
`RUSTFS_TLS_PATH`; publication additionally needs `RUSTFS_TLS_RELOAD_ENABLE=true` and
`RUSTFS_TLS_PUBLISH_DIR` pointing at a private directory outside the TLS material path. And because
RustFS has no route that unpublishes a certificate, destroying the resource only removes it from
state with a warning: the server keeps serving the last published certificate until another
publication replaces it or the server restarts with different material. Import is not supported: the
PEM pair is write-only and RustFS never returns it.

Some capabilities depend on server-side sub-systems and only work once the matching RustFS
sub-system is enabled and configured: KMS (`rustfs_kms_key`), LDAP (`rustfs_ldap_*`), storage tiers
and ILM (`rustfs_tier`, `rustfs_rebalance`), audit targets (`rustfs_audit_target`) and notification
targets (`rustfs_notification_target`, `rustfs_bucket_notification`). Until then the server returns
its own error, which the provider surfaces verbatim.

## Non-goals

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

## Claiming a capability

A route in RustFS's `route_policy.rs` is **not** proof of support. RustFS registers routes before it
implements them and answers `501 NotImplemented` (see above). Before documenting or building a
capability, probe the running server, record the exact RustFS version and the response, and cite that
evidence in the issue or pull request. Do not infer support from the route table, a release note, or
a successful call on a different method or path.

## Provider configuration

| Provider attribute | Environment variable | Description |
|---|---|---|
| `endpoint` | `RUSTFS_ENDPOINT` | RustFS server in `host:port` format (admin API, usually `:9001`) |
| `access_key` | `RUSTFS_USER` | Access key / username |
| `secret_key` | `RUSTFS_SECRET` | Secret key / password |
| `ssl` | — | Use HTTPS for the admin API |
| `insecure` | — | Skip TLS verification (self-signed certificates) |

Environment variables win when both are set. A missing endpoint fails `terraform plan` with an
explicit diagnostic instead of guessing a default.

> **Note:** `access_secret` is the deprecated alias of `secret_key`. Both are accepted; when both
> are set, `secret_key` wins. The alias exists so that configurations written for
> `weinmann-emt/rustfs` keep working — see [Migrating](#migrating-from-weinmann-emtrustfs).

## Resources

Every resource links to its documentation on the Terraform Registry.

<!-- BEGIN AUTOGEN:resources -->
| Resource | Manages |
|---|---|
| [`rustfs_audit_target`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/audit_target) | Manage RustFS audit-log targets (webhook, amqp, kafka, mqtt, nats, postgres, mysql, redis, pulsar) |
| [`rustfs_bucket`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket) | Manage S3 buckets in rustfs |
| [`rustfs_bucket_cors`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_cors) | Manage the CORS configuration of an S3 bucket in rustfs |
| [`rustfs_bucket_durability`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_durability) | Manage the per-bucket durability override in rustfs |
| [`rustfs_bucket_encryption`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_encryption) | Manage RustFS bucket server-side encryption configuration |
| [`rustfs_bucket_lifecycle_configuration`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_lifecycle_configuration) | Manage S3 bucket lifecycle configurations in rustfs |
| [`rustfs_bucket_metadata_backup_import`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_metadata_backup_import) | Import bucket metadata from a ZIP archive |
| [`rustfs_bucket_notification`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_notification) | Manage RustFS bucket event notification configuration |
| [`rustfs_bucket_object_lock`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_object_lock) | Manage RustFS bucket object lock configuration |
| [`rustfs_bucket_on_demand_migration`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_on_demand_migration) | Attach an external S3-compatible source bucket to a local RustFS bucket and pull objects lazily on first read (PUT/GET/DELETE /rustfs/admin/v3/on-demand-migration/{bucket}). Create probes the source Every create and update first sends the configuration with ?dry-run=true: RustFS validates the configuration, probes the source (HeadBucket plus a one-key listing) and writes nothing. Only after that succeeds does the provider save the configuration, so an unreachable source or a wrong key never reaches bucket metadata. Secrets The configuration read is redacted: secret_key, session_token, the Azure account_key/sas_token and the GCS service-account JSON come back as REDACTED. The provider keeps the configured values in state while the server reports them redacted, so a refresh produces no diff. Import sets only the bucket, so an imported resource has no source secret in state until the next apply supplies it; the server never returns the value. Prerequisites The RUSTFS_ON_DEMAND_MIGRATION_ENABLED module switch must be on on every node, and the deployment needs a RustFS license that covers on-demand migration. A gcs_native source additionally needs a server built with the gcs feature. The admin routes exist in every supported release (1.0.1+). Write-back needs namespace locking enabled and exactly one pool with one erasure set. Other topologies can still read through the source, but a pull is refused as unsupported; the provider does not retry it. Destroy Delete clears the configuration idempotently. Already-pulled objects stay in the bucket. Backfill is not managed by this resource: POST /rustfs/admin/v3/on-demand-migration/{bucket}/backfill?op=start\|cancel is a job whose completed_with_failures state has no Terraform equivalent. |
| [`rustfs_bucket_policy`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_policy) | Manage the raw S3 bucket policy document in rustfs |
| [`rustfs_bucket_public_access_block`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_public_access_block) | Manage the block public access configuration of an S3 bucket in rustfs |
| [`rustfs_bucket_recovery`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_recovery) | Recover an orphaned bucket: bucket metadata (or its incarnation sidecar) that survived after the bucket's volume directories were removed outside RustFS, leaving an ordinary CreateBucket to refuse the name. The operation is generation-fenced. incarnation_id is the exact incarnation UUID the orphaned metadata belongs to, sent as expectedIncarnationId; RustFS refuses the recovery when a disk still holds the volume, when any disk is offline or its metadata is unreadable, or when the identity does not match. That UUID, not the bucket name, is what makes the operation safe, so it is also the resource id: bucket names are reusable, and a recovery pinned only to a name could refer to a later, unrelated bucket. Usage Recovery retires the orphan and frees the name; it does not create a bucket. Pair it with a rustfs_bucket for the same name whose depends_on lists this resource, as in the example below. Used alone, the next refresh finds no bucket (Read can only confirm existence), removes this resource from state, and Terraform plans the recovery again. Because the server retires the input UUID, re-sending the request is safe, but the plan never converges. Where the UUID comes from The UUID is not generated by Terraform. Read it from the orphaned bucket's incarnation sidecar, or from a trusted metadata backup taken from that bucket. A diagnostic metadata export is not a restorable backup and does not carry a usable identity. Read and destroy There is no route to read orphan state back: once the name is recreated the bucket is ordinary, so Read can only confirm that the bucket exists. A missing bucket is removed from state without error; an existing one keeps its state as the audit trail. Because the server retires the input UUID and a recreated bucket receives a new generation whose UUID this route does not return, the provider cannot later prove that the live bucket is the one it recovered. Delete is a no-op. Recovery cannot be undone, and the provider never deletes the recovered bucket or its objects: terraform destroy only forgets the record. Remove the bucket itself with rustfs_bucket if that is what you want. Restrictions Recovery is local to one deployment and is refused with OperationAborted while site replication is enabled; reconcile every site before retrying. The caller needs the RecoverOrphanedBucketAction admin action. The route ships in RustFS 1.0.2; on 1.0.1 the failure names the route and the server version. |
| [`rustfs_bucket_replication`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_replication) | Manage RustFS bucket replication configuration |
| [`rustfs_bucket_tags`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_tags) | Manage the tags on an S3 bucket in rustfs |
| [`rustfs_bucket_versioning`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_versioning) | Manage RustFS bucket versioning configuration |
| [`rustfs_config`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/config) | Manage server sub-system configuration (config-kv), the mc admin config equivalent. Each resource manages one sub-system scope. |
| [`rustfs_config_restore`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/config_restore) | Restore a stored server-config snapshot (PUT /rustfs/admin/v3/restore-config-history-kv). This is destructive: the snapshot replaces the whole live configuration, so it can undo an unrelated change another operator made after the snapshot was taken. RustFS saves the current configuration as a new history entry before applying the restore, so the restore itself can be undone through rustfs_config_restore and the entry that request created. The action is one-shot: Read only echoes the recorded result, Delete is a no-op, and changing triggers runs the same restore again. restore_id must name an entry from rustfs_config_history; a missing or legacy entry fails the apply and changes nothing. |
| [`rustfs_group`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/group) | Manage RustFS IAM groups and their members |
| [`rustfs_group_policy_attachment`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/group_policy_attachment) | Attach a canned IAM policy to an IAM group and detach it on destroy, leaving every other policy on the group untouched |
| [`rustfs_iam_backup_import`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/iam_backup_import) | Import RustFS IAM entities (users, groups, policies, service accounts) from a base64-encoded ZIP archive |
| [`rustfs_ilm_transition`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/ilm_transition) | Starts a manual ILM transition run for one bucket (optionally narrowed to a prefix) and keeps the durable job the server returns. This resource moves data A transition copies or moves objects to the named storage tier. A wrong bucket or prefix can move far more data than intended, and moving it back is not a provider operation. dry_run reports what would be enqueued without moving anything; run it first against an unfamiliar lifecycle configuration. Lifecycle Create starts the run in the server's durable-job mode and records job_id; Read reports the job's current state and progress counters. Every configurable attribute carries RequiresReplace, so changing one starts a new run instead of editing the finished one. triggers exists only to re-run a transition with the same parameters. Delete is a no-op: the provider never cancels a run, never rolls a transition back and never mutates ILM state on destroy. terraform destroy only forgets the record. Cancel a run with the admin API (DELETE /rustfs/admin/v3/ilm/transition/jobs/{job_id}) if that is what you want. Missing jobs The server answers NoSuchKey once it no longer holds the job record; at that point Read removes this resource from state without error. Requirements The caller needs the SetTierAction admin action for the run. The routes ship from RustFS 1.0.1 on; an older server fails with a diagnostic naming the route and its version. |
| [`rustfs_integrity_job`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/integrity_job) | Start a bounded RustFS data-integrity job for explicit object versions (POST /rustfs/admin/v3/integrity/{bucket}/jobs) and keep the durable job the server returns. This resource reads object data An audit job reads the listed object versions and verifies their integrity evidence; a migrate job reads them and writes a verified copy under target_key. The read is bounded by the item list (1 to 64 versions) and max_object_bytes, but 64 versions at the limit is heavy I/O. confirm must repeat the bucket name so a job cannot be started against the wrong bucket by accident. Lifecycle RustFS creates a job paused and runs it only after a resume. Create creates the job and resumes it in one apply; Read reports the current state, revision and per-item results. Every configurable attribute carries RequiresReplace, so changing one starts a new job. One integrity worker runs per cluster, so a second resume can be refused with SlowDown while another job is active. Delete sends cancel through the control route. Cancelling is idempotent and does not roll back bytes already verified or migrated; a terminal job is returned unchanged. There is no route that deletes a job record, so destroy only stops the work and forgets the record. Readiness and requirements migrate requires RUSTFS_SHARD_INTEGRITY_WRITE and RUSTFS_SHARD_INTEGRITY_FLEET_CONFIRMED on every node; without them the apply fails before a job is created. Read the rustfs_integrity_readiness data source to inspect those switches. The routes are implemented in RustFS 1.0.1 through 1.0.3; a server that does not implement them produces a version-aware diagnostic. Import Import with <bucket>/<job_id>, because the status route is bucket-scoped. |
| [`rustfs_kms_backup`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/kms_backup) | Create one RustFS KMS backup bundle under the server's configured backup root. This records the bundle's metadata. The server exposes no route to read, verify or delete a specific bundle, so refresh only reports whether the backup subsystem is still available, and destroy only forgets the record; the bundle stays on the server. The create route returns identifiers and metadata only, never key material, so no secret enters state. Supplying an explicit backup_id is required: the server refuses to mix bundles in a non-empty destination, so a deterministic name is what makes a retry fail loudly instead of leaving a second orphan bundle. Restore is not managed by this provider. It destroys and replaces key material, requires the backup id and conflict policy as separate confirmations, and needs the KMS master key out of band. |
| [`rustfs_kms_key`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/kms_key) | Manage RustFS KMS master keys. Deleting a KMS master key is critical and irreversible: it schedules the destruction of the key material and makes every object encrypted under the key permanently unreadable. Guard the key with skip_destroy = true. |
| [`rustfs_kms_rekey`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/kms_rekey) | Start a RustFS bulk rekey sweep over one or more buckets or prefixes. Rekey re-wraps the stored data-key envelopes under the current key-encryption key. It does not rewrite object bodies, does not change ETags or object versions, and never destroys a superseded key version: objects that were already re-wrapped stay readable while the sweep runs and after it is cancelled. Creating this resource starts the sweep and returns once the server accepts it. Refreshing reads the current or most recent sweep, so state, current_bucket and the counters update while it runs. Destroying a running sweep requests cancellation; the cancel is cooperative and does not move already re-wrapped envelopes back. A completed sweep is left alone. RustFS answers 501 when the configured KMS backend cannot re-wrap envelopes: Local, Static and AWS cannot, while Vault KV2 and Vault Transit can. A second sweep is refused while one is running. Import takes the server's job id. The configured buckets and prefix are not recoverable from the server, so set them in configuration after importing. |
| [`rustfs_ldap_policy_attachment`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/ldap_policy_attachment) | Attach a canned policy to an LDAP user or group and detach it on destroy |
| [`rustfs_ldap_service_account`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/ldap_service_account) | Create a service account scoped to an LDAP user |
| [`rustfs_module_switch`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/module_switch) | Manage RustFS feature module switches (notify and audit modules). |
| [`rustfs_notification_target`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/notification_target) | Manage RustFS event-notification targets |
| [`rustfs_object_data_cache_flush`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/object_data_cache_flush) | Flush the RustFS object data cache (POST /rustfs/admin/v3/object-data-cache/flush). Without bucket the whole cache is flushed; with bucket only that bucket; with bucket and object only that identity. The action is one-shot: Read only echoes the recorded result, Delete is a no-op, and changing triggers, bucket or object runs the flush again. Cache tuning stays in the server configuration. |
| [`rustfs_oidc_provider`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/oidc_provider) | Manage one persisted OIDC identity provider used by RustFS Console single sign-on (GET/PUT/DELETE /rustfs/admin/v3/oidc/config). Creating or updating the resource first runs OIDC discovery through POST /rustfs/admin/v3/oidc/validate, so an unreachable config_url or an invalid discovery document fails the apply before anything is persisted. Lockout risk: deleting the last enabled provider can lock operators out of the RustFS Console. The provider emits a warning when it detects that condition. Secrets: the server never returns client_secret. The provider keeps the configured value in state (marked sensitive) while the server reports the secret as configured, and removing it from the configuration does not clear it on the server. Destroy and recreate the provider to remove its secret. |
| [`rustfs_policy`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/policy) | Manage S3 policies |
| [`rustfs_pool_decommission`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/pool_decommission) | Drain and retire a RustFS storage pool. This resource moves data. Creating it starts a decommission: RustFS copies every object of the named pool to the remaining pools and then retires the pool. Retiring the pool from the deployment still requires removing its volume expression from the server configuration and restarting RustFS; this resource only drives the drain and reports its progress. The deployment must have at least two storage pools; RustFS answers 501 NotImplemented on a single-pool deployment. The drain needs enough free space on the remaining pools for the drained data plus overhead, and a decommission cannot run while a rebalance is in progress. Destroying an in-flight drain cancels it and clears the canceled record. Canceling does not move already relocated objects back, and a completed drain cannot be canceled or cleared: destroy leaves the completed record in place. |
| [`rustfs_quota`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/quota) | Manage bucket quota in rustfs |
| [`rustfs_rebalance`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/rebalance) | Triggers a pool rebalancing operation in RustFS and reports its progress. Creating the resource starts a rebalance. Refreshing mirrors GET /rustfs/admin/v3/rebalance/status: the trigger id, the overall state and the per-pool progress, usage and cleanup warnings. A server that does not implement the status route keeps the recorded trigger without failing the refresh. Destroying the resource stops an in-flight rebalance through POST /rustfs/admin/v3/rebalance/stop. Stopping does not move already relocated data back: pools stay partially drained, and stopping a rebalance that already finished is a no-op. The status route is deployment-wide, so the resource only observes and stops the run it started (or the run recorded by the legacy fixed id); a rebalance started outside Terraform is reported and left alone. When the server does not implement the status or stop route, destroy warns and leaves any running rebalance in place. |
| [`rustfs_remote_target`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/remote_target) | Manage remote targets used as replication destinations or notification ARNs in rustfs |
| [`rustfs_scanner_cycle_state_reset`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/scanner_cycle_state_reset) | Run the scanner cycle-state reset (POST /rustfs/admin/v3/scanner/cycle-state/reset). Creating this resource clears the scanner's cycle-recovery bookkeeping and makes the next cycle scan the full namespace; it does not discard usage accounting. The action is one-shot: Read only echoes the recorded result, Delete is a no-op, and changing triggers runs the reset again. The provider retries a busy scanner leader lock a few times before failing. Scanner tuning stays in the scanner subsystem of rustfs_config or in the server environment. |
| [`rustfs_scanner_usage_state_reset`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/scanner_usage_state_reset) | Run the scanner usage-state rebuild (POST /rustfs/admin/v3/scanner/usage-state/reset). This is destructive to accounting: the reset discards the durable usage baseline and publishes a bootstrap-pending marker, and the next complete scanner cycle rebuilds usage from a full pass. Bucket quotas and usage reporting may briefly misreport while that happens. The action is one-shot: Read only echoes the recorded result, Delete is a no-op, and changing triggers runs it again. The route is synchronous; the provider retries a busy scanner leader lock a few times and then fails, so retry the apply after the current cycle ends if the lock stays busy. Scanner tuning stays in the scanner subsystem of rustfs_config or in the server environment. |
| [`rustfs_serviceaccount`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/serviceaccount) | Manage ServiceUser/API Keys |
| [`rustfs_site_replication`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/site_replication) | Manage site-replication peers in rustfs. Each resource manages one peer. Endpoint, credential and TLS changes edit the peer in place; only name forces a replacement, so adding or removing one peer never recreates the others. Use rustfs_site_replication_repair to reconcile a diverged group. |
| [`rustfs_site_replication_repair`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/site_replication_repair) | Repair a RustFS site-replication group. Creating this resource reconciles every peer's site-replication state: the provider asks RustFS for a dry-run plan, executes it, and stores the resulting operation. The route is synchronous, so status is terminal when the apply finishes (success or partial). A partial result reports which site and family still have failures in sites; re-create the resource (for example with terraform taint) to retry. Repair mutates data. It rewrites bucket targets and IAM items on the peers, so confirm must repeat the provider endpoint. RustFS persists a bounded history of operations and prunes the oldest successful ones; when the record for this resource has been pruned, Read keeps the last observed state instead of forgetting the repair and running it again. Destroying this resource does nothing on the server: a repair cannot be undone. It only forgets the operation record. Import sets operation_id from the import ID; the configuration must still carry confirm before the next plan. |
| [`rustfs_table_bucket`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/table_bucket) | Manage the S3 Tables (Iceberg REST catalog) switch of an existing rustfs bucket. Enabling the catalog marks the bucket as a table bucket served at catalog_uri. Destroying this resource only switches the catalog off: the bucket and every object in it are left untouched, but the bucket returns to ordinary lifecycle behaviour (table buckets are excluded from normal expiration). S3 Tables is a preview feature upstream. This resource exposes the switch, not Iceberg client compatibility. |
| [`rustfs_tier`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/tier) | Manage RustFS storage tiers for data transition to external backends |
| [`rustfs_tls_certificate`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/tls_certificate) | Publish the leaf certificate the RustFS default listener serves. Publishing is a whole-file replacement, not a patch, so changing either PEM replaces the resource and publishes the new pair. RustFS accepts the request only on its native TLS listener (ssl = true) and only when started with RUSTFS_TLS_RELOAD_ENABLE=true and RUSTFS_TLS_PUBLISH_DIR pointing at a private directory outside RUSTFS_TLS_PATH. RustFS has no route that removes a published certificate. Destroying this resource therefore only removes it from Terraform state (with a warning); the server keeps serving the last published certificate until another publication replaces it or the server is restarted with different TLS material. Import is not supported: the PEM pair is write-only and the server never returns it, so an imported resource could not describe the certificate it manages. |
| [`rustfs_token_revocation`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/token_revocation) | Revoke a RustFS user's temporary (STS) access keys for one identity provider (POST /rustfs/admin/v3/revoke-tokens/{user_provider}). This action logs users out immediately. Creating the resource deletes every STS credential user holds under user_provider; the next request signed with such a credential fails authentication. The revocation cannot be undone, so the resource has to name both the provider and the user: RustFS would otherwise default to the caller's own identity, and a provider-wide revocation is not what a typo should do. The resource is an action, not managed state: Read only echoes the recorded result, Delete is a no-op, and changing user_provider, user or triggers replaces the resource and revokes again. Revocation is idempotent at the server: with no matching STS credentials left it reports zero revoked and succeeds. |
| [`rustfs_user`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/user) | Manage RustFS user |
| [`rustfs_user_policy_attachment`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/user_policy_attachment) | Attach a canned IAM policy to a user and detach it on destroy |
<!-- END AUTOGEN:resources -->

Some resources perform an action on the server instead of modelling a stored object, so they
deliberately do not support import. Each resource page says whether it does.

## Data sources

<!-- BEGIN AUTOGEN:data-sources -->
| Data source | Returns |
|---|---|
| [`rustfs_batch_jobs`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/batch_jobs) | Lists the MinIO-compatible batch jobs RustFS reports. RustFS implements the batch-job admin surface without a job execution backend: list-jobs always answers with an empty list, and describe-job and status-job always report that the requested job does not exist. Job documents are exposed as the raw JSON the server returned (jobs, job, status) instead of an invented Terraform schema, because the MinIO batch-job payload is large, version-dependent, and not owned by this provider. This data source is read-only; job submission stays an mc/rc operation. |
| [`rustfs_bucket_metadata_backup`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/bucket_metadata_backup) | Export bucket metadata as a ZIP archive |
| [`rustfs_bucket_on_demand_migration_status`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/bucket_on_demand_migration_status) | Read the runtime status of a bucket's on-demand migration configuration (GET /rustfs/admin/v3/on-demand-migration/{bucket}/status). Per-node caveat: this endpoint reports the node that answered the request. Counters, queue depth and breaker state are per-node runtime state, so in a distributed deployment query every node to build a cluster-wide picture. The saved configuration and updated_at are cluster-wide; the provider reads whichever node its endpoint resolves to. Reads of the status endpoint keep working while the module switch is off, so a disabled deployment can still be inspected. |
| [`rustfs_bucket_usage`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/bucket_usage) | Read the scanner's usage accounting for one bucket or prefix (GET /rustfs/admin/v3/usage/{bucket}). The numbers are not real-time: the scanner publishes them after a cycle, the endpoint caches a computed answer for up to 30 seconds, and sets_reporting below sets_total means at least one erasure set could not contribute, so the counters are a lower bound. A newly created bucket can report zeros until the first cycle covers it. An unknown bucket fails the read instead of reporting zeros. The read also performs an S3 HeadBucket existence check, so the provider credentials need s3:ListBucket (or equivalent) on the bucket in addition to the admin permission that serves the usage route. |
| [`rustfs_cluster_snapshot`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/cluster_snapshot) | Read-only cluster snapshot from the RustFS v4 admin API (GET /rustfs/admin/v4/cluster/snapshot). The snapshot is a large, deployment-shaped payload and is returned as raw JSON so the provider follows the server instead of pinning every field. The v4 admin API is a preview surface; a server that predates it answers 511 Upgrade Required. |
| [`rustfs_config_history`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/config_history) | Read the config snapshots RustFS stores before every configuration mutation (GET /rustfs/admin/v3/list-config-history-kv). RustFS saves the whole server configuration, not a per-subsystem diff, so every entry describes the same scopes; there is no per-subsystem filter and the route records no actor. data is the snapshot in the same directive text as get-config-kv with secret-like values replaced by the server (data_redacted), and it is marked sensitive because the snapshot can still hold values the server does not classify as secrets. Pass restore_id to rustfs_config_restore to roll back to an entry. |
| [`rustfs_extensions`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/extensions) | Extension catalog and instances from the RustFS v4 admin API (GET /rustfs/admin/v4/extensions/catalog and GET /rustfs/admin/v4/extensions/instances). Both payloads are returned as the server's JSON verbatim so the provider does not invent a schema the server has not committed to. The v4 admin API is a preview surface; a server that predates it answers 511 Upgrade Required. |
| [`rustfs_groups`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/groups) | Lists all IAM groups for RustFS |
| [`rustfs_health_info`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/health_info) | Cluster health info and OBD diagnostics for RustFS |
| [`rustfs_iam_backup`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/iam_backup) | Export all RustFS IAM entities (users, groups, policies, service accounts) as a base64-encoded ZIP archive |
| [`rustfs_iam_policies`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/iam_policies) | List all canned IAM policies available on the RustFS cluster |
| [`rustfs_iam_policy`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/iam_policy) | Fetch the details of a single canned IAM policy by name |
| [`rustfs_ilm_expiry_status`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/ilm_expiry_status) | Exposes the RustFS lifecycle expiry worker status: whether expiry is enabled, how fresh the scanner is, the expiry queue counters, maintenance control, data-movement pause and the scan catch-up estimate. Read-only; it never changes ILM state. |
| [`rustfs_ilm_recovery_records`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/ilm_recovery_records) | Reads the durable ILM recovery controls the server keeps for interrupted transitions and tier deletions. Recovery controls are the server's own repair queue, not Terraform state: this data source is read-only and never retries, abandons or exports a record. |
| [`rustfs_ilm_tier_stats`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/ilm_tier_stats) | Exposes per-tier ILM storage statistics (object counts and sizes) from RustFS. |
| [`rustfs_integrity_inventory`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/integrity_inventory) | One page of a bucket's per-object shard-integrity inventory (GET /rustfs/admin/v3/integrity/{bucket}/inventory). The route lists object versions and reads each version's metadata; it does not read object payloads. A page holds at most 100 versions, so a large bucket needs the returned next_key_marker and next_version_marker to page through it. The checksums shown here are inventory evidence, not a migration acceptance decision: a rustfs_integrity_job compares against them only when the item supplies an independent expected_sha256. |
| [`rustfs_integrity_readiness`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/integrity_readiness) | Shard-integrity readiness reported by GET /rustfs/admin/v3/integrity/readiness: whether protected writes are requested and activated on this node, whether the fleet operator attested to the rollout, and which requirements RustFS still considers unverified. A migrate integrity job only runs when new_writes_enabled_here is true, which needs both RUSTFS_SHARD_INTEGRITY_WRITE and RUSTFS_SHARD_INTEGRITY_FLEET_CONFIRMED on every node. |
| [`rustfs_kms_backup_status`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/kms_backup_status) | Read whether the RustFS server can export a KMS backup bundle, and which bundle responsibility the configured backend can claim. This reports identifiers, paths and readiness only; it never returns key material. A false value for export_supported means backup is disabled until the server's RUSTFS_KMS_BACKUP_DIR, RUSTFS_KMS_BACKUP_KEK and RUSTFS_KMS_BACKUP_KEK_ID are set. |
| [`rustfs_kms_config`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/kms_config) | Fetch the current RustFS KMS backend configuration, including the backend type, cache settings, and default key ID. |
| [`rustfs_kms_status`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/kms_status) | Fetch the current RustFS KMS service status, including backend health, cache stats, capabilities, and cluster config. |
| [`rustfs_metrics`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/metrics) | Metrics stream for the RustFS server. Requires a server that implements GET /rustfs/admin/v3/metrics: RustFS 1.0.1 through 1.0.3 register the route but answer 501 NotImplemented, which Terraform reports as an unsupported capability naming the server version and the route. |
| [`rustfs_object_data_cache_stats`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/object_data_cache_stats) | Statistics of the RustFS object data cache (GET /rustfs/admin/v3/object-data-cache/stats). A deployment without a cache reports mode disabled and zero counters. This data source only observes the cache; its tuning stays in the server configuration. |
| [`rustfs_oidc_config`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/oidc_config) | Read every effective OIDC provider RustFS reports (GET /rustfs/admin/v3/oidc/config), including providers configured through server environment variables. Set provider_id to return only that provider. The server never returns client secrets; client_secret_configured reports whether one is stored. |
| [`rustfs_plugins`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/plugins) | Plugin catalog and instances from the RustFS v4 admin API (GET /rustfs/admin/v4/plugins/catalog and GET /rustfs/admin/v4/plugins/instances). Both payloads are returned as the server's JSON verbatim: the v4 plugin schema is new and stays opaque so a newer server does not require a provider release. Secret values in instance configuration are redacted by the server. The v4 admin API is a preview surface; a server that predates it answers 511 Upgrade Required. |
| [`rustfs_pools`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/pools) | List all RustFS storage pools and their status |
| [`rustfs_quota`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/quota) | Read the quota of a RustFS bucket |
| [`rustfs_replication_diff`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/replication_diff) | Reads the object versions of a bucket whose replication is still pending or has failed. RustFS computes the diff on demand by scanning object versions, bounded to 10,000 versions per call; is_truncated reports that the scan stopped early, so re-run with a narrower prefix. The bucket needs a replication configuration. This data source is read-only: it never retries, resyncs or mutates replication state. The counters are read from the aggregate response shape, a RustFS extension that reports scan coverage; the madmin-compatible stream has no envelope to carry it. |
| [`rustfs_replication_metrics`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/replication_metrics) | Exposes replication transfer metrics (counts, bytes, queue backlog) for a bucket from RustFS. |
| [`rustfs_replication_mrf`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/replication_mrf) | Reads the failed-replication (most-recent-failure) backlog counters of a bucket. The counters come from the aggregate response shape, which carries no object names or version ids and only needs the narrow GetReplicationMetrics admin action; the default madmin stream enumerates object identities and needs ReplicationDiff. The durable ledger is cluster-shared, so the counters cover the whole cluster. This data source is read-only: MRF records are operational data, not configuration, and it never retries or drains them. |
| [`rustfs_runtime_capabilities`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/runtime_capabilities) | Runtime capabilities advertised by the RustFS v4 admin API (GET /rustfs/admin/v4/runtime/capabilities). storage_classes is the versioned storage-class write contract: check contract_version before gating writes on supported_write_classes. The v4 admin API is a preview surface in RustFS 1.0.3; json keeps the full payload so a newer server can add fields without a provider release. A server that predates the v4 namespace answers 511 Upgrade Required, which Terraform reports as a negotiation diagnostic naming the route and the server version. |
| [`rustfs_scanner_status`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/scanner_status) | Runtime status of the RustFS object scanner (GET /rustfs/admin/v3/scanner/status): cycle state, the last completed cycle and its counters, usage publication state, and data-movement pause state. The scanner's tuning knobs stay in the scanner subsystem of rustfs_config or in the server environment; this data source only observes them. |
| [`rustfs_server_info`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/server_info) | Exposes cluster, server, pool and drive information from the RustFS admin API. |
| [`rustfs_storage_info`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/storage_info) | Storage layout and health information for the RustFS cluster, including the storage backend and a per-drive breakdown. |
| [`rustfs_temporary_accounts`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/temporary_accounts) | List the temporary (STS) access keys RustFS reports, optionally including service accounts. RustFS has no route that lists every temporary account: temporary-account-info takes one access key, so the listing comes from list-access-keys-bulk. The provider reads that route once per identity provider to attribute each key: the idp/ldap and idp/openid routes only report keys whose claims match, so a key the generic route reports and neither scoped route does is builtin. The server enumerates IAM users, so an external identity that RustFS does not persist as an IAM user is only listed when parent_user names it. No secret key or session token is ever returned for a listed access key. Set include_policies to read each key's effective policy as well; that costs one lookup per key. |
| [`rustfs_tls_certificate_state`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/tls_certificate_state) | Read the TLS certificate the RustFS default listener serves, including the publication revision and fingerprints. The route is only available on RustFS 1.0.2 or later, and only on the native TLS listener (ssl = true). |
| [`rustfs_user_mfa`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/user_mfa) | Fetch the second-factor (MFA) status of a RustFS user by access key |
| [`rustfs_users`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/users) | List all RustFS IAM users, optionally filtered by bucket name |
<!-- END AUTOGEN:data-sources -->

`rustfs_quota` intentionally exists both as a resource (per-bucket limit) and as a data source
(read it back).

## Examples

| Path | What it shows |
|---|---|
| [`examples/complete`](./examples/complete) | A full configuration: bucket, IAM, policies, encryption, lifecycle, notification |
| [`examples/resources/<resource>`](./examples/resources) | One `resource.tf` per resource, used to generate the docs |
| [`examples/data-sources/<data source>`](./examples/data-sources) | One `data-source.tf` per data source |
| [`examples/provider`](./examples/provider) | Provider configuration |

`make validate-examples` checks every example against the provider schema in CI.

## Migrating from `weinmann-emt/rustfs`

This provider grew out of [`weinmann-emt/terraform-provider-rustfs`](https://github.com/weinmann-emt/terraform-provider-rustfs)
(with thanks to its authors). The overlap was kept schema-compatible on purpose, so migrating is a
state rewrite rather than a refactor: 12 of their 16 resources and all 6 data sources need **no**
configuration change.

1. Point your configuration at this provider:

   ```terraform
   terraform {
     required_providers {
       rustfs = {
         source  = "caberdo/rustfs"
         version = "~> 0.7"
       }
     }
   }
   ```

2. Rewrite the provider address in your state:

   ```bash
   terraform state replace-provider weinmann-emt/rustfs caberdo/rustfs
   ```

3. Confirm nothing else changed:

   ```bash
   terraform init -upgrade
   terraform plan      # expect: No changes.
   ```

| Their resource | Here | Migration |
|---|---|---|
| `rustfs_bucket`, `rustfs_bucket_versioning`, `rustfs_bucket_lifecycle_configuration`, `rustfs_bucket_notification`, `rustfs_bucket_object_lock`, `rustfs_group`, `rustfs_group_policy_attachment`, `rustfs_iam_backup_import`, `rustfs_ldap_service_account`, `rustfs_serviceaccount`, `rustfs_tier`, `rustfs_user` | same names | **No change** — the schemas are supersets of theirs |
| `rustfs_bucket_quota` | `rustfs_quota` | Rename in the configuration, then `terraform state mv rustfs_bucket_quota.x rustfs_quota.x` |
| `rustfs_policy` | `rustfs_policy` | The statement attribute is spelled `resource` (upstream's `ressource` typo is not carried over) |
| `rustfs_bucket_encryption` | `rustfs_bucket_encryption` | Schema restructured around `sse_algorithm` / `apply_server_side_encryption_by_default` instead of `algorithm` |
| `rustfs_bucket_replication` | `rustfs_bucket_replication` | Schema restructured around nested `destination` and `rule` blocks instead of flat `destination_bucket` / `delete_replication` |

All six of their data sources (`iam_backup`, `iam_policies`, `iam_policy`, `pools`, `user_mfa`,
`users`) exist here under the same names with the same attributes.

## Verifying a release

Every release is built by [GoReleaser](https://github.com/goreleaser/goreleaser) from a GPG-signed,
GitHub-verified tag, and ships:

| Asset | Purpose |
|---|---|
| `terraform-provider-rustfs_<version>_<os>_<arch>.zip` | The provider binaries |
| `terraform-provider-rustfs_<version>_SHA256SUMS` | Checksums for all artifacts |
| `terraform-provider-rustfs_<version>_SHA256SUMS.sig` | GPG signature over the checksums |
| `terraform-provider-rustfs_<version>_provenance.intoto.jsonl` | SLSA v1.0 build provenance over every checksummed artifact |
| `terraform-provider-rustfs_<version>_manifest.json` | Terraform registry manifest (plugin protocol versions) |
| `sbom.spdx.json` | SPDX software bill of materials |

The release signing key is committed as [`.github/release-signing-key.asc`](./.github/release-signing-key.asc).
Replace `<version>` with the release you are verifying — for example `0.7.0` for the current release:

```bash
gpg --import .github/release-signing-key.asc     # fingerprint 77BEB69E5C039CB69017B060DFE36652ADCC2259
gpg --verify terraform-provider-rustfs_<version>_SHA256SUMS.sig \
             terraform-provider-rustfs_<version>_SHA256SUMS
```

The checksum signature proves the checksums are ours; the build provenance proves *where the
binaries come from*. It is generated in the release workflow by
[`actions/attest-build-provenance`](https://github.com/actions/attest-build-provenance) and stored in
the repository's attestation record, so it is checkable with the GitHub CLI:

```bash
gh attestation verify terraform-provider-rustfs_<version>_linux_amd64.zip \
    --repo caberdo/terraform-provider-rustfs
```

That answers *which repository, tag, commit and workflow produced this archive*, and it fails if
the file was rebuilt or modified after the fact. The same provenance is attached to the release as
`terraform-provider-rustfs_<version>_provenance.intoto.jsonl`, so a release you already downloaded
verifies without a lookup in the attestation record:

```bash
gh attestation verify terraform-provider-rustfs_<version>_linux_amd64.zip \
    --repo caberdo/terraform-provider-rustfs \
    --bundle terraform-provider-rustfs_<version>_provenance.intoto.jsonl
```

Terraform itself verifies the checksums when it installs a provider and records them in
`.terraform.lock.hcl`; commit that file so every `terraform init` re-checks the same artifacts.
On the GitHub side, the release tag shows the **Verified** badge because it is signed by a key
registered to the releasing account.

## Testing and coverage

| Command | What it runs |
|---|---|
| `make test` | Unit tests (the `TestAcc*` suite skips itself without `TF_ACC`) |
| `make coverage` | Unit tests plus a per-package coverage table; `make coverage-html` opens a report |
| `make testacc-docker` | The full acceptance suite against RustFS in Docker — this is what CI runs |
| `make testacc-live` | Starts RustFS with `podman-compose` and runs the acceptance suite against it |
| `make coverage-merge` | Merges the unit profile with the acceptance profile (`coverage-acceptance.out`, written by the acceptance container) into one statement-coverage table |

The suite covers every `pkg/rustfs` client method and every resource lifecycle path. Four fuzz
targets replay their committed seed corpus, and the live CRUD tests skip themselves when
`RUSTFS_ENDPOINT` is unset. Acceptance tests run against real RustFS servers on the
`.github/rustfs-versions.txt` matrix (`1.0.1`, `1.0.2`, `1.0.3` and `latest`); a few skip when the
server cannot provide what they need (no LDAP identity provider is configurable on RustFS `1.0.x`,
a single-pool deployment has nothing to rebalance, and the metrics stream is not implemented). CI
runs the unit tests with `-race`, then the acceptance matrix; the acceptance job merges both
coverage profiles and publishes a per-package table in the job summary of every run. A weekly
`Fuzz` workflow runs each fuzz target for a minute — deeper campaigns are `make fuzz FUZZTIME=10m`.

Statement coverage of this repository, merged from the unit and acceptance profiles:

<!-- coverage:start -->
```
pkg/rustfs  ████████████████████  97.8%   floor 97.3%
provider    ██████████████████░░  88.0%   floor 87.5%
total       ██████████████████░░  90.2%   floor 89.5%   milestones 80% and 90% met
```

Measured on `f84538b` (unit + acceptance) on 2026-10-19.
<!-- coverage:end -->

The floor is enforced: `scripts/coverage-gate.sh` fails the `Coverage` check on a pull request whose
merged coverage drops below `.github/coverage-baseline.txt`, and the `latest` cell of the acceptance
matrix repeats that gate on `main`. The same check fails when this block drifts from the baseline.
To raise the floor, run `make coverage` and `make coverage-merge`, update
`.github/coverage-baseline.txt`, then `make coverage-readme`.

The unit-only `provider` figure is a measuring artefact rather than a gap: that package is thin
CRUD wiring whose behaviour is asserted end-to-end against a real server, which is why the
acceptance suite is instrumented too (`-covermode=atomic -coverpkg=./...` in
`acc_test/docker-compose.yml`) and why the two profiles are merged. Run `make coverage` (and
`make testacc-docker` followed by `make coverage-merge` for the second number) for the exact
figures of your checkout.

## Development

```bash
make build              # build ./bin/terraform-provider-rustfs
make install            # install into ~/.terraform.d/plugins for local dev_overrides
make test coverage      # unit tests + coverage table
make testacc-docker     # acceptance suite, Docker
make lint vet fmt       # golangci-lint (.github/golangci.yml), go vet, gofmt
make generate           # regenerate docs/ with terraform-plugin-docs
make validate-examples  # check examples/ against the provider schema
```

To use a local build with Terraform, add to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "caberdo/rustfs" = "/path/to/terraform-provider-rustfs/bin"
  }
}
```

`main` is protected: every change lands through a pull request with green CI, and
[`AGENTS.md`](./AGENTS.md) describes the repository conventions (schema rules, coding patterns,
how resources are added, and the settled flat package layout).

## Troubleshooting and FAQ

**A resource fails with an error from the server, e.g. about KMS, LDAP, tiers or targets.**
Those features live in RustFS sub-systems that must be enabled and configured first. The provider
does not silently skip them; it surfaces the server's message.

**`rustfs_tier` fails with "Remote tier probe outcome is uncertain".**
The server probes the tier's backend before saving it, so the `endpoint` must be reachable from the
server, must not be the deployment itself (loopback is refused with "not allowed: loopback
address"), and the `bucket` named in `config_json` must already exist there. `config_json` also
carries the backend type as a top-level `type` and its credentials as `accessKey`/`secretKey` —
see the resource example.

**Do I still need `access_secret`?**
No. Use `secret_key` (or `RUSTFS_SECRET`). `access_secret` keeps working as a deprecated alias.

**`terraform plan` shows no changes but I expected some.**
Several resources are "action style" (they perform work once and keep no server-side object):
`rustfs_iam_backup_import`, `rustfs_bucket_metadata_backup_import` and `rustfs_rebalance`. They
cannot be imported and do not drift.

**Site replication fails on a single-node server.**
RustFS refuses loopback outbound targets (`outbound URL host 'localhost' is not allowed: loopback
host`). Use an address the server can actually reach.

**Why does deleting a bucket that is already gone not fail?**
Read and delete are idempotent: an object that disappeared out-of-band is removed from state
instead of erroring.

**Which server version should I run?**
CI exercises `1.0.1` and `latest`; see `.github/rustfs-versions.txt` for the list that is known to
work.

**Can I use OpenTofu?**
Yes — this is a plugin-protocol 6.0 provider, and it is listed in the
[OpenTofu registry](https://search.opentofu.org/provider/caberdo/rustfs) as well.

## Security, contributing and support

- [`SECURITY.md`](./.github/SECURITY.md) — how to report a vulnerability, and what the provider does
  and does not guarantee.
- [`CONTRIBUTING.md`](./.github/CONTRIBUTING.md) — how to get a change merged.
- [`GOVERNANCE.md`](./GOVERNANCE.md) — who decides what, and how continuity is handled.
- [`ROADMAP.md`](./ROADMAP.md) — what the project intends to do next (and what it will not do).
- [`ARCHITECTURE.md`](./ARCHITECTURE.md) — how the provider is built, at runtime and as a release.
- [`SUPPORT.md`](./SUPPORT.md) — where to ask questions.
- [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md) — the ground rules for the community.
- [`CHANGELOG.md`](./CHANGELOG.md) — what changed in every release.
- [`.github/OPENSSF-BEST-PRACTICES.md`](./.github/OPENSSF-BEST-PRACTICES.md) — the evidence behind the
  project's OpenSSF Best Practices badge answers.

## License

[Apache-2.0](LICENSE) — see [`NOTICE`](./NOTICE) for the projects this one derives from.
