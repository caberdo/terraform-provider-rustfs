# Architecture

High-level design of the Terraform provider for RustFS. Repository conventions live in
[`AGENTS.md`](./AGENTS.md); this file describes the shape of the running program and where the
trust boundaries are (the security argument is in [`.github/SECURITY.md`](./.github/SECURITY.md)).

## What the program is

A Terraform provider plugin (plugin protocol 6.0, built on
`terraform-plugin-framework` v1.19.0) published as
`registry.terraform.io/caberdo/rustfs`. Terraform launches the compiled binary and talks to it
over the plugin protocol; the provider never runs inside the Terraform process.

```
Terraform CLI ──plugin protocol 6.0──► terraform-provider-rustfs (one process)
                                            │
                        ┌───────────────────┴───────────────────┐
                        │  *AllClient (provider/all_client.go)   │
                        └───────┬───────────────────────┬───────┘
                                │                       │
                    S3.S3 (*s3.Client)        RustClient (pkg/rustfs)
                    AWS SDK for Go v2         admin HTTP client, SigV4
                                │                       │
                                ▼                       ▼
                     RustFS S3 API :9000     RustFS admin API :9001
                     (buckets, policies,      /rustfs/admin/v3
                      tags, CORS, …)          (+ a few non-v3 routes)
```

## Design decisions

Design decisions that define what the provider deliberately does not model are recorded here. The
user-facing short form is the non-goals table in `README.md` and `templates/index.md.tmpl`; this
section is the authority when the two disagree.

### Objects — out of scope ([#157](https://github.com/caberdo/terraform-provider-rustfs/issues/157))

The provider does not manage S3 object bodies or per-object metadata. There is no `rustfs_object`
resource and no object data source, now or in the current roadmap. (`rustfs_object_data_cache_stats`
and `rustfs_object_data_cache_flush` control the server's object cache; they do not manage
objects.)

RustFS objects are application data, not infrastructure. The provider's job is the control plane:
buckets and their configuration, IAM, KMS, replication, tiers, targets, server configuration, and
the observability of jobs that run on the server. `rustfs_bucket` still lists and deletes objects
when `force_destroy = true`; that is bucket lifecycle, and the resource models the bucket, not its
contents.

**Rejected alternatives.** A `rustfs_object` resource would exist to compare hashes and overwrite
when they differ, but state holds metadata, not bytes: a wrong `source_hash`, a multipart ETag or
a server that recomputes the ETag after encryption turns that comparison into silent data loss or
a permanent diff. Large objects make plan/apply slow and state large, `content` puts secrets in
state (marking it `Sensitive` hides it from output, not from the state file), and `destroy`
becomes a delete request. Object presence is also not a stable local fact: on-demand migration
pulls lazily, an integrity migrate job writes a verified copy, and a versioned object has an
identity rather than a single "current" body. The full S3 object surface (multipart, checksums,
storage classes, encryption, tags, ACLs, retention, copy, versioning) is larger than any resource
shipped so far. A read-only object data source returning metadata and a presigned URL was rejected
for the same reason the lifecycle is unrepresentable: the URL is time-limited and the metadata is
already available with `mc stat` or the S3 API.

The questions a future proposal must answer are recorded so the decision can be reopened rather
than extended one attribute at a time:

| Question | Why it has no safe answer for a Terraform resource |
|---|---|
| Conflict policy when the object exists | The resource cannot tell "created by Terraform" from "created out of band" without storing a marker; a refresh that cannot find the marker reports drift or deletes data. |
| Drift detection (`ETag` vs `Last-Modified`) | `ETag` is not a content hash for multipart or encrypted objects, and `Last-Modified` changes on metadata updates. Neither proves the body is the one the configuration describes. |
| Versioning | Addressing the current version silently overwrites a versioned object; addressing a `version_id` requires the version to exist before apply. |
| Secrets in `content` | State is plaintext JSON; `Sensitive` does not remove the value from the state file or its backups. |
| On-demand and integrity migration | The object is present but not local, or is being copied to a verified key; a plan that treats either as drift pulls or overwrites data as a side effect. |

**Consequences.** No `rustfs_object*` type is added to the registry; no implementation issue is
opened. The shared Markdown changes are handed to lane A in
[#178](https://github.com/caberdo/terraform-provider-rustfs/issues/178): replace the opener "The
provider models objects with a Terraform lifecycle" with "The provider models RustFS control-plane
and configuration state with Terraform resources; it does not manage S3 object bodies", and append
this non-goals row:

| Area | Not modeled | Why it is out of scope |
|---|---|---|
| Object data plane | S3 object bodies and per-object metadata; a `rustfs_object` resource or object data source | RustFS objects are application data, not infrastructure. Terraform state would hold metadata and hashes, not bodies, so an object resource would exist to track drift: a wrong `source_hash` or `etag` can silently overwrite data, large objects make plan/apply slow and state large, and `content` puts secrets in state. Object presence is also not a stable local fact: on-demand migration pulls lazily, an integrity migrate job writes a verified copy, and versioning means "the current object" is not necessarily the object a previous apply wrote. Use `mc`, the AWS provider pointed at RustFS, or the application's own upload path. `rustfs_bucket` still deletes objects as part of `force_destroy`, which is bucket lifecycle, not object management. |

### Heal — status observed, runs out of scope ([#158](https://github.com/caberdo/terraform-provider-rustfs/issues/158))

Two halves, decided separately:

1. **Heal runs are declined.** Starting, querying and cancelling a heal
   (`POST /rustfs/admin/v3/heal/*`, including the `clientToken` status query and `forceStop`
   cancel) is an operator repair action, not desired state. The v4 MRF responsibility actions
   (`POST /rustfs/admin/v4/heal/mrf/responsibilities/actions`) are declined for the same reason.
2. **Heal status is observed.** Three read-only data sources are in scope, in follow-up
   [#177](https://github.com/caberdo/terraform-provider-rustfs/issues/177):
   `rustfs_background_heal_status` over `POST /rustfs/admin/v3/background-heal/status`, and
   `rustfs_heal_mrf_responsibilities` / `rustfs_heal_replacement_recovery` over the two v4 `GET`
   routes. They join `rustfs_scanner_status`, `rustfs_ilm_expiry_status` and
   `rustfs_replication_mrf` as observability, not lifecycle.

The probe (2026-10-27; `rustfs/rustfs:1.0.1`, `:1.0.2`, `:1.0.3` and `:latest`, which reported
`refs/tags/1.0.4`, one single-node container each, SigV4 admin credentials):

| Route | Method | 1.0.1 | 1.0.2 | 1.0.3 | 1.0.4 | Observed |
|---|---|:--:|:--:|:--:|:--:|---|
| `/rustfs/admin/v3/heal/` | POST | 200 | 200 | 200 | 200 | Returns `clientToken` immediately. Root heal needs `recursive = true` (400 otherwise); an empty body is 400 "err request body parse". |
| `/rustfs/admin/v3/heal/{bucket}` | POST | 200 | 200 | 200 | 200 | Start, then `?clientToken=…` reports progress and `&forceStop=true` cancels. |
| `/rustfs/admin/v3/heal/{bucket}/{prefix}` | POST | 200 | 200 | 200 | 200 | Same start/status/cancel, scoped to the prefix. |
| `/rustfs/admin/v3/background-heal/status` | POST | 200 | 200 | 200 | 200 | Healer state, queue/admission counters and coverage. Two consecutive calls were byte-identical and changed nothing: read-only. |
| `/rustfs/admin/v4/heal/mrf/responsibilities` | GET | 200 | 200 | 200 | 200 | `contractVersion: 1`, `nodeLocal: true`, empty list. Read-only. |
| `/rustfs/admin/v4/heal/mrf/responsibilities/actions` | POST | 400 | 400 | 400 | 400 | Empty and nil-UUID bodies are rejected before mutation; the `Refresh`/`Recheck`/`AcceptUnverifiedRisk` branches mutate queue state in `heal.rs`, and the single-node probe had no legacy responsibility to exercise them. |
| `/rustfs/admin/v4/heal/replacement-recovery` | GET | 200 | 200 | 200 | 200 | `contractVersion: 2`, definitive local and cluster status, empty records. Read-only. |

`dry_run` is not side-effect free: a dry-run root start raised
`healOperations.admission.accepted` from 6 to 7. The run is asynchronous, has no list route and no
server-owned job id independent of the caller's token. A wrong method answers `426 Upgrade
Required`, not `405`; clients must use the exact method above.

**Rejected alternatives.** A one-shot `rustfs_heal` resource (option B) has no honest lifecycle:
the provider's job resources wrap a server-returned durable job with an id, status route and
terminal state, while heal returns a caller token, has no list route, and its result is repaired
object data Terraform cannot read back. `destroy` would either cancel a repair from an unrelated
lifecycle event or only forget the run; `dry_run` still admits work; and `Read` could only ask the
token for its current status. Declining the whole surface (option C) was rejected because the
background status route is a POST in name only: it is idempotent, has no body and changes nothing,
and it belongs next to the existing status data sources. The v4 read routes are read-only
snapshots with contract versions, so they join the same follow-up rather than being misclassified
as operator actions.

**Consequences.** No run control is exposed, and `terraform destroy` can never start or cancel a
heal. No `rustfs_heal*` type is added before follow-up #177. The shared Markdown changes are in
[#178](https://github.com/caberdo/terraform-provider-rustfs/issues/178): replace the existing v4
heal/MRF non-goal row with one that names only the actions route, and add this row:

| Area | Not modeled | Why it is out of scope |
|---|---|---|
| Heal runs | `POST /rustfs/admin/v3/heal/`, `POST /rustfs/admin/v3/heal/{bucket}`, `POST /rustfs/admin/v3/heal/{bucket}/{*prefix}`, and their `clientToken` status and `forceStop` queries | Starting, querying or cancelling a heal is an operator repair action over object data, not desired state. A run has no server-listable durable record, and `terraform destroy` must not be able to cancel a repair by accident. `dry_run` still admits a task: on 1.0.1–1.0.4 a dry-run start raised the admission counter. The read-only background healer status is tracked for a data source in #177. Use `mc admin heal`, the console or the deployment tooling to start or cancel a run. |

## Entry point and wiring

| Stage | Code | What happens |
|---|---|---|
| Process start | `main.go` | `providerserver.Serve` with `Address = registry.terraform.io/caberdo/rustfs`. `-debug` attaches a debugger. `version` is injected by GoReleaser `-ldflags -X main.version=…`, `dev` locally. |
| Provider identity | `provider/provider.go` `Metadata` | `TypeName = "rustfs"`, version from the binary. |
| Schema | `provider/provider.go` `Schema` | Six optional attributes: `endpoint`, `access_key`, `secret_key` (sensitive), `access_secret` (deprecated alias, sensitive), `ssl`, `insecure`. |
| Configure | `provider/provider.go` `Configure` | Resolves each attribute through `envOrDefault` (`RUSTFS_ENDPOINT`, `RUSTFS_USER`, `RUSTFS_SECRET` win over the block), fails with an explicit diagnostic when the endpoint is empty, then builds `*AllClient` and hands the *same* value to both `resp.ResourceData` and `resp.DataSourceData`. |
| Registration | `provider/registry.go` `registerResource()` / `registerDataSource()`, called from `init()` in each type's own file | Every resource and data source self-registers; `provider/provider.go` only returns the registry. Every resource implements `ResourceWithImportState` except the nine that are actions or write-only: `rustfs_bucket_metadata_backup_import`, `rustfs_config_restore`, `rustfs_iam_backup_import`, `rustfs_kms_backup`, `rustfs_object_data_cache_flush`, `rustfs_scanner_cycle_state_reset`, `rustfs_scanner_usage_state_reset`, `rustfs_tls_certificate` and `rustfs_token_revocation`. |

`AllClient` (`provider/all_client.go`) is the whole runtime state a resource gets: an `*s3.Client`
and a `rustfs.RustfsAdmin`. Resources are structs holding that pointer; there is no global state.

## The two API clients

| | `client.S3` (`*s3.Client`) | `client.RustClient` (`rustfs.RustfsAdmin`) |
|---|---|---|
| Built in | `Configure`, from `awsconfig.LoadDefaultConfig` with a static credentials provider, region `us-east-1` | `rustfs.New` from `RustfsAdminConfig` |
| Talks to | RustFS S3 API on the same endpoint | RustFS admin API at `{scheme}://{endpoint}/rustfs/admin/v3` |
| Signing | AWS SDK v2 signer | `aws/signer/v4` `SignHTTP`, payload SHA-256, service `s3`, region `us-east-01` (`pkg/rustfs/admin_client.go`) |
| Endpoint style | `BaseEndpoint` + `UsePathStyle = true` (no DNS-per-bucket) | own `http.Client` + `net/http` request builder |
| Escape hatch | — | `DoDirectRequest` strips the admin base path and targets any other route on the server (metrics, health) |

Both clients share one `http.Transport` built by `newHTTPTransport(secure)`:
proxy from the environment, 30 s dial/keep-alive, 1 min response-header and idle timeouts, and,
when `ssl = true`, `MinVersion: TLS 1.2` plus an optional extra CA bundle from `SSL_CERT_FILE`.
`insecure` disables certificate verification and is intended for self-signed test servers only.

## Request flow

```
resource.tf ──► Terraform core ──► framework request (Plan/State/Config)
                                        │
                        req.Plan.Get(ctx, &model)   ← typed model struct, tfsdk tags
                                        │
                        client.RustClient.Do(...)   or   client.S3.<Operation>(...)
                                        │
                        resp.State.Set(ctx, &model) → Terraform state (secret_key is sensitive)
```

Conventions that matter for maintainers:

* Model structs are declared next to their resource file and use `types.String`/`types.Bool`/
  `types.Set` with `tfsdk` tags; sets cross the client boundary through
  `stringSetFromSlice`/`stringSliceFromSet`.
* Diagnostics are appended and then checked (`resp.Diagnostics.HasError()`), never ignored.
* Computed attributes are set on every path or carry `UseStateForUnknown()`.
* Read/Delete are idempotent: a resource that disappeared out-of-band is removed from state
  instead of failing.

## Error model

* `pkg/rustfs` parses non-2xx admin/S3 bodies into `*APIError{Code, Message, StatusCode, Raw}`.
  `Error()` returns the raw body, so callers that string-match `NoSuchBucket`,
  `NoSuchCORSConfiguration` or `404` keep working.
* `404`, `405` and `501` on a version-gated admin route are mapped to an
  `UnsupportedFeatureError` naming the feature, the route and the detected server version
  (`GET /rustfs/admin/v3/healthinfo`). RustFS registers routes before implementing them and
  answers `501 NotImplemented`, so "route exists" is not "route works".

## Repository layout

```
main.go                        entry point, //go:generate tfplugindocs
provider/                      flat package `provider`
  provider.go                  schema, Configure, Resources(), DataSources()
  all_client.go                AllClient{S3, RustClient}
  helper.go                    env resolution, model helpers
  rustfs_<name>_resource.go    one resource per file
  rustfs_<name>_datasource.go  one data source per file (legacy: _data_source.go)
  *_test.go                    schema/metadata unit tests + TestAcc* acceptance tests
pkg/rustfs/                    admin API client, one file per API area
docs/                          generated by tfplugindocs from examples/ + templates/ — never hand-edit
examples/                      the documentation source (examples/resources/<type>/resource.tf, …)
acc_test/docker-compose.yml    RustFS + test-runner services used by CI and `make testacc-*`
scripts/                       validate-examples, coverage summary
.github/                       workflows, golangci/goreleaser/dependabot configs, SECURITY/CONTRIBUTING
```

The flat layout above is authoritative. The `internal/` proposal tracked in
[#2](https://github.com/caberdo/terraform-provider-rustfs/issues/2) was dropped and its attempt
branches were never merged; a future move needs its own accepted issue and must keep Terraform type
names (`rustfs_bucket`, …) unchanged, because they are part of the provider's public contract.

## Test layers

| Layer | Location | Needs a server | What it proves |
|---|---|---|---|
| Unit (client) | `pkg/rustfs/*_test.go` | no — `httptest` mocks the admin API | request shape, signing, error parsing |
| Unit (provider) | `provider/*_test.go`, `-skip '^TestAcc'` | no | schema, metadata, model helpers |
| Acceptance | `provider/*_test.go`, `TestAcc*` with `TF_ACC=1` | yes — RustFS in Docker/Podman | real CRUD, drift, import, capability gaps |
| Live CRUD | `pkg/rustfs/*_test.go` behind `RUSTFS_ENDPOINT` | yes | the admin client against a real server |

Coverage is collected in both layers (`coverage.out` for unit, `coverage-acceptance.out` from the
container run) because the `provider` package asserts its behaviour end-to-end against a server.
See [`.github/OPENSSF-BEST-PRACTICES.md`](./.github/OPENSSF-BEST-PRACTICES.md) for the numbers as
they stand and `README.md` for the commands.

## Release pipeline

`Create Release Tag` (GPG-signed tag, `workflow_dispatch`) → `Release` GoReleaser build for
freebsd, windows, linux and darwin on amd64/386/arm/arm64 (darwin/386 and windows/arm{,64} are
excluded), `SHA256SUMS` signed with the release key
(`.github/release-signing-key.asc`), registry manifest and SPDX SBOM attached to the GitHub
release. `main` is protected by the `protect-main` ruleset: pull requests only, `Lint & Security`
and `Test` required, no force-push, no deletion, stale reviews dismissed.
