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
