# AGENTS.md

## Project: terraform-provider-rustfs

A Terraform provider for [RustFS](https://github.com/rustfs/rustfs), an S3-compatible object
storage system, built with the Plugin Framework (v1.19.0) and Go 1.27.1. The provider serves
both the S3 API (through the AWS SDK for Go v2) and the RustFS admin API (`/rustfs/admin/v3`).

Registry address: `registry.terraform.io/caberdo/rustfs`.

## Resources and Data Sources

| Resource | Go file | Manages |
|---|---|---|
| `rustfs_user` | `provider/rustfs_user_resource.go` | IAM users |
| `rustfs_policy` | `provider/rustfs_policy_resource.go` | S3/IAM policies |
| `rustfs_serviceaccount` | `provider/rustfs_service_account_resource.go` | Service accounts / API keys |
| `rustfs_ldap_service_account` | `provider/rustfs_ldap_service_account_resource.go` | Service accounts scoped to an LDAP user |
| `rustfs_bucket` | `provider/rustfs_bucket_resource.go` | S3 buckets |
| `rustfs_quota` | `provider/rustfs_quota_resource.go` | Per-bucket quota limits |
| `rustfs_user_policy_attachment` | `provider/rustfs_user_policy_attachment_ressource.go` | Canned policy on an IAM user |
| `rustfs_group_policy_attachment` | `provider/rustfs_group_policy_attachment_resource.go` | Canned policy on an IAM group |
| `rustfs_ldap_policy_attachment` | `provider/rustfs_ldap_policy_attachment_ressource.go` | Canned policy on an LDAP user/group |
| `rustfs_bucket_policy` | `provider/rustfs_bucket_policy_ressource.go` | Raw S3 bucket policy document |
| `rustfs_bucket_public_access_block` | `provider/rustfs_bucket_public_access_block_ressource.go` | Block-public-access configuration |
| `rustfs_bucket_tags` | `provider/rustfs_bucket_tags_ressource.go` | Bucket tags |
| `rustfs_bucket_cors` | `provider/rustfs_bucket_cors_ressource.go` | Bucket CORS rules |
| `rustfs_bucket_versioning` | `provider/rustfs_bucket_versioning_resource.go` | Bucket versioning |
| `rustfs_bucket_object_lock` | `provider/rustfs_bucket_object_lock_resource.go` | Object lock / retention |
| `rustfs_bucket_encryption` | `provider/rustfs_bucket_encryption_resource.go` | SSE-S3 / SSE-KMS encryption |
| `rustfs_bucket_lifecycle_configuration` | `provider/rustfs_bucket_lifecycle_configuration_resource.go` | Lifecycle rules |
| `rustfs_bucket_notification` | `provider/rustfs_bucket_notification_resource.go` | Event notification targets |
| `rustfs_bucket_replication` | `provider/rustfs_bucket_replication_resource.go` | Cross-bucket replication |
| `rustfs_bucket_durability` | `provider/rustfs_bucket_durability_ressource.go` | Per-bucket durability override |
| `rustfs_site_replication` | `provider/rustfs_site_replication_ressource.go` | Site-replication peers |
| `rustfs_remote_target` | `provider/rustfs_remote_target_ressource.go` | Remote replication/notification targets |
| `rustfs_rebalance` | `provider/rustfs_rebalance_resource.go` | Pool rebalancing |
| `rustfs_group` | `provider/rustfs_group_resource.go` | IAM groups with members |
| `rustfs_tier` | `provider/rustfs_tier_resource.go` | Storage tiers (S3, Azure, GCS, …) |
| `rustfs_kms_key` | `provider/rustfs_kms_key_ressource.go` | KMS master keys |
| `rustfs_config` | `provider/rustfs_config_ressource.go` | Server sub-system configuration (config-kv) |
| `rustfs_module_switch` | `provider/rustfs_module_switch_ressource.go` | Feature module switches |
| `rustfs_audit_target` | `provider/rustfs_audit_target_ressource.go` | Audit-log webhook/HTTP targets |
| `rustfs_iam_backup_import` | `provider/rustfs_iam_backup_import_resource.go` | IAM import from backup |
| `rustfs_bucket_metadata_backup_import` | `provider/rustfs_bucket_metadata_backup_import_resource.go` | Bucket metadata import from backup |

| Data Source | Go file | Returns |
|---|---|---|
| `rustfs_pools` | `provider/rustfs_pools_datasource.go` | Storage pools |
| `rustfs_users` | `provider/rustfs_users_datasource.go` | IAM users |
| `rustfs_groups` | `provider/rustfs_groups_data_source.go` | IAM groups |
| `rustfs_user_mfa` | `provider/rustfs_user_mfa_datasource.go` | MFA status of a user |
| `rustfs_iam_backup` | `provider/rustfs_iam_backup_datasource.go` | IAM export (ZIP) |
| `rustfs_iam_policies` | `provider/rustfs_iam_policies_datasource.go` | Canned IAM policies |
| `rustfs_iam_policy` | `provider/rustfs_iam_policy_datasource.go` | A single canned IAM policy |
| `rustfs_quota` | `provider/rustfs_quota_datasource.go` | Bucket quota |
| `rustfs_bucket_metadata_backup` | `provider/rustfs_bucket_metadata_backup_datasource.go` | Bucket metadata export (ZIP) |
| `rustfs_metrics` | `provider/rustfs_metrics_data_source.go` | Metrics stream |
| `rustfs_health_info` | `provider/rustfs_health_info_data_source.go` | Cluster health / OBD diagnostics |
| `rustfs_storage_info` | `provider/rustfs_storage_info_data_source.go` | Cluster storage info |
| `rustfs_server_info` | `provider/rustfs_server_info_data_source.go` | Cluster, server, pool and drive info |
| `rustfs_replication_metrics` | `provider/rustfs_replication_metrics_datasource.go` | Replication transfer metrics |
| `rustfs_ilm_tier_stats` | `provider/rustfs_ilm_tier_stats_datasource.go` | Per-tier ILM statistics |
| `rustfs_kms_config` | `provider/rustfs_kms_config_datasource.go` | KMS backend configuration |
| `rustfs_kms_status` | `provider/rustfs_kms_status_datasource.go` | KMS service status |

`rustfs_quota` intentionally exists as both a resource and a data source. Terraform type names
are part of the provider's public contract — never rename one (it breaks existing state); the
migration below only moves Go files and identifiers.

## Architecture

```
main.go                       # providerserver entry point, version set via goreleaser ldflags
provider/                     # flat package `provider`
├── provider.go               # provider schema, Configure, Resources(), DataSources()
├── all_client.go             # AllClient{S3 *s3.Client, RustClient rustfs.RustfsAdmin}
├── helper.go                 # envOrDefault, secret_key/access_secret resolution
├── rustfs_<name>_resource.go # resource (legacy spelling: _ressource.go)
├── rustfs_<name>_datasource.go # data source (legacy spelling: _data_source.go)
└── *_test.go                 # schema/metadata unit tests + TestAcc* acceptance tests
pkg/rustfs/                   # RustFS admin API client (package `rustfs`)
├── admin_client.go           # RustfsAdmin, RequestData, SigV4 signing, doRequest/DoDirectRequest
└── <name>.go                 # one file per API area (bucket.go, kms.go, user_account.go, …)
docs/                         # generated by tfplugindocs — never hand-edit
examples/                     # provider/, resources/<tf name>/resource.tf, data-sources/<tf name>/data-source.tf
acc_test/docker-compose.yml   # RustFS + golang test-runner services used by CI acceptance tests
tools/                        # build-tagged tools shim keeping terraform-plugin-docs in go.mod
```

### Migration in progress (epic #2)

The repository is being aligned with the `terraform-provider-gravitino` layout in
[caberdo/terraform-provider-rustfs#2](https://github.com/caberdo/terraform-provider-rustfs/issues/2).
Until the child issues land, the current paths above are authoritative; afterwards expect:

| Current | Target |
|---|---|
| `pkg/rustfs` (package `rustfs`) | `internal/client` (package `client`) |
| `provider/rustfs_*_resource.go` | `internal/resources/<name>/resource.go` |
| `provider/rustfs_*_datasource.go` | `internal/datasources/<name>/` |
| inline model structs in `provider/` | `internal/models/` |
| `provider/provider.go` registration | `internal/provider/provider.go` (thin) |
| `acc_test/docker-compose.yml` | root `docker-compose.yml` + `internal/acceptance/` live harness |

### Two clients

`Configure` builds an `*AllClient` and hands it to both `resp.ResourceData` and
`resp.DataSourceData`:

- `client.S3` (`*s3.Client`) — S3 API calls (buckets, policies, tags, CORS, versioning, …), built from
  an `aws.Config` with a static credentials provider, `BaseEndpoint` and `UsePathStyle`.
- `client.RustClient` (`rustfs.RustfsAdmin`) — RustFS admin API (`/rustfs/admin/v3`), SigV4-signed
  with the AWS SDK v2 signer (`aws/signer/v4`, service `s3`, region `us-east-01`);
  `DoDirectRequest` targets paths outside the admin base
  (for example `/rustfs/admin/v3` is stripped, used for metrics/health endpoints).

Provider attributes: `endpoint`, `access_key`, `secret_key` (sensitive), `access_secret`
(deprecated alias of `secret_key`), `ssl`, `insecure`. Each is overridden by
`RUSTFS_ENDPOINT`, `RUSTFS_USER`, `RUSTFS_SECRET` respectively; environment variables win when
both are set. A missing endpoint fails `Configure` with an explicit diagnostic.

## Conventions

### Quick Reference

**Configure (mandatory):**

```go
client, ok := req.ProviderData.(*AllClient)
if !ok {
    resp.Diagnostics.AddError(
        "Unexpected Resource Configure Type",
        fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
    )
    return
}
r.client = client
```

**Plan/state handling (mandatory):** append diagnostics, then bail out on error.

```go
var plan bucketCorsModel
resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
if resp.Diagnostics.HasError() {
    return
}
```

**Error reporting (mandatory):** a human title plus a contextual detail string.

```go
resp.Diagnostics.AddError(
    "Error creating bucket CORS configuration",
    "Could not create CORS configuration: "+err.Error(),
)
```

**Logging:** `tflog.Trace`/`tflog.Debug` at CRUD boundaries, e.g.
`tflog.Trace(ctx, "created a bucket CORS configuration resource")`.

**Idempotent read/delete:** a resource that is already gone must not fail. Read removes the
resource from state and Delete returns without error:

```go
if err != nil {
    if strings.Contains(err.Error(), "NoSuchCORSConfiguration") ||
        strings.Contains(err.Error(), "NoSuchBucket") ||
        strings.Contains(err.Error(), "404") {
        resp.State.RemoveResource(ctx)
        return
    }
    resp.Diagnostics.AddError(...)
    return
}
```

**Never:** hand-edit `docs/` (generated), rename a Terraform type or attribute (breaks state),
or drop a schema change silently — an attribute that cannot be updated in place must carry
`RequiresReplace()`.

### Patterns

- **Client errors** are plain `errors.New(string(body))` produced by `doRequest`/`DoDirectRequest`
  when the HTTP status is not 2xx (`>299` and `!=200 && !=204` respectively). There is no typed
  error yet, so "not found" is detected by matching the server payload
  (`NoSuchBucket`, `NoSuchCORSConfiguration`, …) or the literal `404` in `err.Error()`.
- **IDs**: simple resources use `ImportStatePassthroughID` on the bucket/name attribute and mirror
  it into `id`; composite resources parse a separator-delimited import ID and report an explicit
  diagnostic when it is malformed.
- **Computed attributes** must be set on every code path or carry `UseStateForUnknown()`;
  otherwise apply fails with "Provider produced inconsistent result after apply".
- **Sets vs slices**: schema sets are converted with local helpers
  (`stringSetFromSlice` / `stringSliceFromSet`) before being sent to the API.
- **Enum attributes** use `stringvalidator.OneOf(...)` with every value the RustFS API accepts.
- **Sensitive attributes** (secret keys, tokens) must be marked `Sensitive: true`.
- **Every attribute carries a `Description`**; tfplugindocs copies it into `docs/`.
- **Examples** are the documentation source: `examples/resources/<tf name>/resource.tf`,
  `examples/data-sources/<tf name>/data-source.tf`, `examples/provider/provider.tf`.

### Checklist for New Resources

- [ ] API methods in `pkg/rustfs/<name>.go` (admin) and/or via `client.S3` (S3)
- [ ] `provider/rustfs_<name>_resource.go` implementing `resource.Resource` and
      `resource.ResourceWithImportState`
- [ ] Registered in `provider/provider.go` `Resources()`
- [ ] Schema attributes described, sensitive fields marked, enums validated
- [ ] Unit tests: schema + metadata (no server needed)
- [ ] Acceptance test `TestAcc<Name>...` using `testAccProtoV6ProviderFactories` and
      `testAccPreCheck`
- [ ] Example in `examples/resources/rustfs_<name>/resource.tf`
- [ ] `make generate` to refresh `docs/resources/<name>.md`

### Commands

- **Build**: `go build ./...`
- **Vet**: `go vet ./...`
- **Lint**: `golangci-lint run` (config `.golangci.yml`)
- **Unit tests (client)**: `go test ./pkg/rustfs/... -v` — most files mock the admin API with
  `httptest`; the live CRUD tests dial `RUSTFS_ENDPOINT` and skip themselves when it is unset
- **Unit tests (provider, no server)**: `go test ./provider/... -v -skip '^TestAcc'`
- **Acceptance tests (Docker, as CI runs them)**:
  `docker compose -f acc_test/docker-compose.yml up -d` then
  `docker compose -f acc_test/docker-compose.yml --profile test run --rm test`
  (filter with `-e TEST_FILTER=TestAccBucketResource`)
- **Acceptance tests (local, Podman)**:
  `podman-compose -f acc_test/docker-compose.yml up -d` and
  `RUSTFS_ENDPOINT=127.0.0.1:9001 RUSTFS_USER=rustfsadmin RUSTFS_SECRET=rustfsadmin TF_ACC=1 go test -v ./provider -run TestAcc`
- **Docs**: `make generate` (runs the `//go:generate` tfplugindocs directive in `main.go`)

### Testing

**Client unit tests** (`pkg/rustfs`) mock the admin API with `httptest.NewServer` and a
`RustfsAdminConfig` pointed at the test server; no RustFS instance is required. Payloads should be
copied from the RustFS admin API responses, not invented from the Go structs.

**Provider tests** split into schema/metadata unit tests (`Test<Name>Schema`,
`Test<Name>Metadata` — run without `TF_ACC`) and acceptance tests (`TestAcc...`) that use
`resource.ParallelTest` with `testAccProtoV6ProviderFactories`
(`providerserver.NewProtocol6WithError(New("test")())`) and the `testAccPreCheck` helper, which
requires `TF_ACC`, `RUSTFS_ENDPOINT`, `RUSTFS_USER` and `RUSTFS_SECRET`.

**Live CRUD tests:** `admin_client_test.go`, `lifecycle_test.go`, `policy_test.go`,
`quota_test.go`, `service_account_test.go` and `user_account_test.go` read `RUSTFS_ENDPOINT`,
`RUSTFS_USER` and `RUSTFS_SECRET` and dial the real endpoint. They skip themselves when those are
unset, so `go test ./...` is green without a server; run the Docker/Podman environment to exercise
them live before claiming a CRUD change works.

### Dependencies

- Go 1.27.1
- `terraform-plugin-docs` v0.25.0 (docs generation, via `//go:generate` in `main.go`)
- `terraform-plugin-framework` v1.19.0, `terraform-plugin-framework-validators` v0.19.0
- `terraform-plugin-testing` v1.16.0
- `github.com/aws/aws-sdk-go-v2` with `.../config`, `.../credentials`, `.../service/s3` and
  `.../aws/signer/v4` (S3 client + SigV4 signer)
- `github.com/ProtonMail/go-crypto` (KMS/encryption helpers)
