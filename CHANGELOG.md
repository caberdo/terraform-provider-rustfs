## Unreleased

## 0.8.0 (2026-10-27)

FEATURES:
- `rustfs_bucket` gains `force_destroy` (default `false`). With `force_destroy = true` a destroy
  aborts every incomplete multipart upload, deletes the objects, then deletes the object versions and
  delete markers of a versioned bucket, and only then removes the bucket — so a bucket an application
  filled can be destroyed in one `terraform destroy`. The attribute deletes data and is never enabled
  implicitly; without it RustFS still refuses with `BucketNotEmpty`, and destroy stays idempotent when
  the bucket disappeared out of band.
- `rustfs_bucket` now also exists as a read-only data source, and `rustfs_buckets` lists every bucket. The
  single-bucket data source reports `exists` (a missing bucket is `false` without an error, so it can
  drive `count`/`for_each`), `creation_date` from `ListBuckets` and `region` from `GetBucketLocation`.
  `rustfs_buckets` returns one `ListBuckets` result sorted by name instead of issuing a request per
  bucket, and both fail with a diagnostic naming `s3:ListAllMyBuckets`/`s3:ListBucket` when the
  credentials lack the permission rather than returning an empty list.
- New read-only data sources for the admin read surfaces: `rustfs_tiers` lists the configured storage tiers with the backend type and the backend configuration (every credential value is redacted to `REDACTED`, by the server and again by the provider), `rustfs_kms_keys` lists KMS master keys (metadata only, no key material), and `rustfs_service_accounts` lists the IAM service accounts through the new `GET /rustfs/admin/v3/list-service-accounts` route. The service-account listing differs from `rustfs_temporary_accounts`: that data source reports STS temporary keys, this one reports the persisted IAM accounts, and it never exposes a secret key.
- Four bucket sub-resources are now modelled: `rustfs_bucket_website` (index/error documents, redirect-all
  and routing rules), `rustfs_bucket_logging` (target bucket and prefix, and the target grants), and
  `rustfs_bucket_accelerate` and `rustfs_bucket_request_payment`. All four round-trip on RustFS 1.0.1
  through 1.0.3, so a plan after apply is empty; `rustfs_bucket_accelerate` models only `status`,
  because that is the only field the S3 accelerate document carries. Destroying them removes the configuration (logging is
  disabled; acceleration is set to `Suspended`; request payment returns to `BucketOwner`) and never
  deletes the bucket. `rustfs_bucket_logging` refuses a target bucket that does not exist instead of
  silently logging into nowhere.
- `rustfs_bucket` now applies a change to `acl` or `grant_read`/`grant_write`/`grant_read_acp`/
  `grant_write_acp`/`grant_full_control` in place with `PutBucketAcl` instead of replacing the bucket,
  which used to delete every object for a setting. RustFS 1.0.x accepts canned ACLs and grants but
  `GetBucketAcl` always reports only the owner's `FULL_CONTROL` grant, so the provider keeps the
  configured value in state; an out-of-band ACL change therefore produces no plan, and the attribute
  descriptions say so. The new `rustfs_bucket_policy_status` data source reads
  `GetBucketPolicyStatus`; RustFS reports a bucket public only for a statement granting `s3:*` on the
  bucket to an explicit `{"AWS": ["*"]}` principal, and the data source documents that rule.
- `rustfs_site_replication_status` reads the whole site-replication group
  (`GET /rustfs/admin/v3/site-replication/info`,
  `GET /rustfs/admin/v3/site-replication/status` and
  `GET /rustfs/admin/v3/site-replication/metainfo`): which peers exist, each
  peer's `deployment_id`, sync state and TLS/ILM settings, per-site replicated and
  total counters for every replicated family, unreachable peers with their error,
  the resync retry queue, and the topology counts from the metainfo route. The
  metainfo payload itself stays out of state; only counts and identity are
  reported. A server that answers the status route as unimplemented still yields
  the info and metainfo result with `status_available = false`; any other status
  error fails the read.

  `rustfs_site_replication_resync` drives
  `PUT /rustfs/admin/v3/site-replication/resync/op` with `operation = "start"`,
  `"cancel"` or `"status"`. A resync re-copies the replicated buckets between
  sites, so it can move a large amount of data; every operation names the peer by
  `deployment_id`, which `rustfs_site_replication_status` reports. Every
  configurable attribute carries RequiresReplace, so a change runs a new operation
  instead of editing the recorded one. Destroying the resource does nothing on the
  server: cancelling is an explicit `operation = "cancel"` apply, never a side
  effect of `terraform destroy`.
- `rustfs_config_history_clear` deletes every stored server-config snapshot
  through `DELETE /rustfs/admin/v3/clear-config-history-kv`. This removes all
  rollback points, so `rustfs_config_restore` can no longer reach a snapshot and
  an earlier configuration mistake can no longer be undone through the provider.
  The route returns an empty 200 with no count, so the resource records only
  `cleared_at`; `dry_run` and `entries_cleared` do not exist. The resource is an
  action: Read echoes the recorded result, Delete is a no-op, and `confirm` must
  be the literal `clear-config-history`.
- `rustfs_quota_stats` reads what the quota subsystem sees for a bucket
  (`GET /rustfs/admin/v3/quota-stats/{bucket}`): the configured hard quota, its
  current usage, the remaining headroom and the usage percentage. Unlike
  `rustfs_bucket_usage`, which reports the scanner's eventually-consistent
  accounting, this is the view quota admission uses and may be reservation/ledger
  based. A field the server cannot report is null, never zero, and a missing
  bucket fails the read instead of reporting zeros.

  `rustfs_quota_check` evaluates whether an operation would be admitted
  (`POST /rustfs/admin/v3/quota-check/{bucket}`) for `PUT`, `POST`, `COPY` or
  `DELETE` at a given size. The check is read-only: it changes no quota, object or
  usage. Unknown operation types are reported by RustFS as PUT; the provider only
  accepts the four documented values.
- The admin client now retries transient server answers instead of failing the
  apply on the first `429`, `500`, `502`, `503`/`ServiceUnavailable`, `504`,
  `SlowDown` or `RequestTimeout`, with three bounded, jittered backoffs and a
  `Retry-After` header honoured. Retries are limited to reads and the routes
  RustFS defines as idempotent (the scanner resets, a rebalance stop, a quota
  set and a config set), so no job, credential or delete is replayed. A cancelled Terraform run stops the retry loop immediately.

  Both API clients now send `terraform-provider-rustfs/<version>` in their
  `User-Agent`, so server logs and bug reports can be tied to a provider build.

IMPROVEMENTS:
- `rustfs_kms_rekey` now explains a refused start instead of quoting the server:
  when RustFS answers `501` because the configured KMS backend cannot re-wrap
  envelopes, the diagnostic names the `POST /rustfs/admin/v3/kms/keys/rekey`
  route, the backend and the server version, and says which backends can
  (`Vault KV2` and `Vault Transit`). `rustfs_kms_backup` checks
  `GET /rustfs/admin/v3/kms/backup` before writing: a server that reports
  `export_supported = false` now fails the apply with a diagnostic naming
  `RUSTFS_KMS_BACKUP_DIR`, `RUSTFS_KMS_BACKUP_KEK` and `RUSTFS_KMS_BACKUP_KEK_ID`
  instead of attempting the `POST` and echoing its raw payload.

BUG FIXES:
- `endpoint` accepts `host`, `host:port` and `scheme://host[:port]`, and both API clients are now
  built from one normalized reading of it. Before this change an endpoint that carried its own
  scheme, for example `RUSTFS_ENDPOINT=https://rustfs.example.com`, produced a working S3 client and
  an admin client that dialled `http://https://rustfs.example.com`. An explicit `http`/`https`
  scheme now selects the transport; when `ssl` is also set and contradicts the scheme the plan fails
  with a diagnostic naming both values, and an endpoint with a path, query or inline credentials is
  rejected instead of being pasted into a URL. Configurations that set `ssl` and omit the scheme are
  unchanged.

## 0.7.0 (2026-10-22)

BUG FIXES:
- `rustfs_bucket_lifecycle_configuration` now accepts a `rule` list whose value is
  unknown at plan time, so the resource works when the rules come from `count`,
  `for_each` or any computed value. A known list that still holds an unknown rule
  element is accepted too. The `rule` attribute was backed by a native Go slice
  that could not hold an unknown value, so such a configuration failed to decode
  with "Received unknown value ... Target Type: []provider.ruleModel"; it is now a
  framework list decoded per boundary. An unknown rule value that unexpectedly
  survives to apply is refused instead of being written to the bucket as an empty
  rule list.

## 0.6.1 (2026-10-21)

BUG FIXES:
- The provider now compiles for 32-bit targets. The data-integrity job's byte limits
  (default and maximum object size) are typed as `int64` instead of relying on the
  platform `int`, which is 32 bits on `arm` and `386` and cannot hold 5 GiB. This
  only fixes the build: 64-bit users see no behaviour change, and no state or
  schema moved.

## 0.6.0 (2026-10-21)

FEATURES:
- `rustfs_table_bucket` manages the S3 Tables (Iceberg REST catalog) switch of an existing bucket:
  `PUT`, `GET` and `DELETE /iceberg/v1/buckets/{bucket}` through the direct-request client, exposing
  `enabled`, `catalog_type`, `warehouse`, `catalog_entry_present` and `catalog_uri`, with the bucket
  name as the import ID. Destroying the resource only switches the catalog off — the bucket and its
  objects stay, but the bucket returns to ordinary expiry behaviour. RustFS `1.0.1` does not
  implement the disable route, so destroy there warns, names the route and drops the resource from
  state; `1.0.2` and `1.0.3` disable it. A disabled or missing catalog is removed from state, so the
  next plan recreates it. The live probe proving both `us-east-1` and `us-east-01` credential scopes
  work on these routes is recorded in the issue, and a unit test pins the emitted scope. No data
  source is added
- Add the `rustfs_bucket_on_demand_migration` resource and the
  `rustfs_bucket_on_demand_migration_status` data source to attach an external
  S3-compatible source bucket to a local RustFS bucket and pull objects lazily on
  first read. Both need RustFS 1.0.1 or later, the
  `RUSTFS_ON_DEMAND_MIGRATION_ENABLED` module switch on every node, and a license
  that covers on-demand migration. Creating or updating a configuration probes
  the source with a dry run first, and the server returns the stored source
  secrets redacted, so the provider keeps the configured values in state.
- Add `rustfs_scanner_status` and `rustfs_object_data_cache_stats` data sources plus the
  one-shot `rustfs_scanner_cycle_state_reset`, `rustfs_scanner_usage_state_reset` and
  `rustfs_object_data_cache_flush` actions. The reset and flush resources record the server
  result, keep Read as a state echo, leave Delete empty, and rerun only when `triggers` (or the
  flush scope) changes. `rustfs_scanner_usage_state_reset` requires `confirm_full_rebuild = true`
  because it discards the durable usage baseline and quotas can misreport until a full scanner
  pass rebuilds it; the reset actions retry a busy scanner leader lock a few times before failing.
  Scanner and cache tuning stays in `rustfs_config` or the server environment.
  The routes are registered in the RustFS 1.0.1 through 1.0.3 source; an unsupported route is
  reported with the server version and the missing route.
- `rustfs_ilm_transition` starts a manual ILM transition run for a bucket and
  prefix, keeps the durable job the server returns, and reports its state and
  progress counters on refresh. Every configurable attribute forces a new run,
  and `triggers` exists to re-run one with the same parameters. Destroy is a
  no-op: the provider never cancels a run, never rolls a transition back and never
  mutates ILM state, so `terraform destroy` only forgets the record.

  `rustfs_ilm_expiry_status` reads the lifecycle expiry worker status, and
  `rustfs_ilm_recovery_records` reads the durable recovery controls for one
  protocol (`transition_transaction`, `tier_delete_journal` or
  `tier_delete_manifest`). Both are read-only. The routes ship from RustFS 1.0.1
  on; an older server fails with a diagnostic naming the route and its version.
- `rustfs_rebalance` now observes and stops the rebalance it starts. Creating the
  resource still starts a run, but refresh mirrors
  `GET /rustfs/admin/v3/rebalance/status` into the new computed `status`,
  `pools`, `stopped_at` and `stop_propagation` attributes, and destroy calls
  `POST /rustfs/admin/v3/rebalance/stop` on a run that is still active. Stopping
  does not move already relocated data back, so pools stay partially drained. On
  a server that does not implement the routes, refresh keeps the recorded trigger
  and destroy warns instead of failing. Because the status route is
  deployment-wide, the resource only observes and stops the run it started; a
  rebalance started outside Terraform is reported and left alone. The resource id
  is now the trigger id the server reports; existing state files keep working and
  are migrated in place from the legacy fixed id.
- `rustfs_kms_key` now manages key descriptions, tags, the 7–30 day deletion window and an explicit rotation trigger. New `rustfs_kms_rekey` and `rustfs_kms_backup` resources and a `rustfs_kms_backup_status` data source cover KMS re-keying and backup. Restore is intentionally not exposed: it is destructive and requires confirmations Terraform cannot supply.
- `rustfs_site_replication_repair` reconciles a diverged site-replication group. The
  resource runs the server's dry-run plan, executes it, and records the operation
  in state, including per-site and per-family task counts and errors. Repair
  mutates bucket targets and IAM items on every peer, so `confirm` must repeat
  the provider endpoint (the cluster being repaired); a `partial` result is reported as a warning and kept in state for
  inspection. Destroying the resource only forgets the record, because a repair
  cannot be undone.

  `rustfs_site_replication` continues to edit endpoint, credential and TLS
  changes in place and to remove one peer without recreating the group. Its
  client methods now name the public routes explicitly (`EditPeer`, `RemovePeer`):
  the similarly named `site-replication/peer/edit`, `peer/remove` and
  `peer/edit-capabilities` routes are the peer-side delivery of that fan-out, not
  public operations. The diagnostic and internal routes (`devnull`, `netperf`,
  `rotate-svc-acct`, `peer/bucket-ops`, `peer/iam-item`, `peer/bucket-meta`) stay
  out of the provider: they have no declarative end state.
- `rustfs_integrity_job` starts a bounded RustFS data-integrity job for 1 to 64
  explicit object versions. `Create` creates the server's durable job and resumes
  it, `Read` reflects its state and per-item results, and `Delete` sends `cancel`,
  which is idempotent and does not roll back bytes already verified or migrated.
  `rustfs_integrity_readiness` and `rustfs_integrity_inventory` read the
  deployment's write-readiness switches and a bucket's per-object inventory. The
  routes are implemented in RustFS 1.0.1 through 1.0.3; an older server produces a
  version-aware diagnostic.
- Add the `rustfs_temporary_accounts` data source and the
  `rustfs_token_revocation` resource to inspect and revoke the STS credentials
  RustFS issues. The data source lists temporary access keys through the bulk
  access-key routes, attributes each key to `builtin`, `ldap` or `openid`, and
  never exposes a secret key or session token. Set `include_policies` to read
  each key's effective policy, implied-policy flag and account status (they stay
  null otherwise, because the bulk listing does not report them), and
  `include_service_accounts` to see long-lived service accounts in the same
  listing.

  `rustfs_token_revocation` deletes every STS credential one user holds under one
  identity provider. Revocation is immediate and cannot be undone: the next
  request signed with a revoked credential fails authentication. Both the
  provider and the user are required so a mistake cannot revoke the caller's own
  tokens or sweep a whole provider. Read echoes the recorded result, Delete is a
  no-op, and changing `triggers`, `user` or `user_provider` revokes again. Both
  types need RustFS 1.0.1 or later and fail with a version-aware diagnostic on an
  older server.
- Add the `rustfs_batch_jobs`, `rustfs_replication_diff` and `rustfs_replication_mrf` data sources. `rustfs_batch_jobs` lists the MinIO-compatible batch jobs and can describe/status one id, exposing the server's job documents as raw JSON because RustFS ships the admin surface without a job execution backend (the list is empty and a requested id reports `found = false`). `rustfs_replication_diff` reads the object versions whose replication is pending or failed and reports scan truncation, and `rustfs_replication_mrf` reads the failed-replication backlog counters. Both replication data sources use the aggregate response shape, which carries no object identities and needs only the metrics admin action. No job submission resource is added: the MinIO batch-job payload has no stable Terraform-shaped schema, and job submission stays an `mc`/`rc` operation.
- The admin API client is now version-aware. `RequestData.Version` selects the
  admin namespace (`v3` when empty, so every existing request keeps its current
  URL) and the client stores the host root and the admin base separately. The
  provider treats a `511 Upgrade Required` answer for an unmatched v4 path as a
  negotiation signal rather than a payload, so a pre-v4 server is reported as
  "upgrade RustFS or use the v3 route" naming the route and server version, while
  `501 NotImplemented` keeps its existing unimplemented-route diagnostic.

  New preview data sources read the RustFS 1.0.3 v4 admin API:
  `rustfs_runtime_capabilities` exposes the versioned storage-class write
  contract, and `rustfs_plugins`, `rustfs_extensions` and
  `rustfs_cluster_snapshot` expose their payloads as the server's JSON verbatim
  so the provider does not pin a schema upstream has not committed to. No
  plugin-instance resource is added: the v4 payload is still new.
- `rustfs_bucket_usage` reads the scanner's per-bucket (and per-prefix) usage
  accounting, documenting that the numbers lag the scanner cycle and that
  `sets_reporting` below `sets_total` makes them a lower bound.
  `rustfs_config_history` lists the whole-server config snapshots RustFS stores
  before every mutation, with the server's redacted directive text.
  `rustfs_config_restore` restores one named snapshot: the restore overwrites the
  whole live configuration, so `confirm` must repeat `restore_id`, Read only
  echoes the record and Destroy is a no-op. No existing `rustfs_config` schema
  changed.

BUG FIXES:
- `rustfs_tier` can be imported without planning to replace the tier. `Read` now fills `tier_type`
  from the server's tier listing, so importing a tier that already exists no longer leaves the
  required `tier_type` empty and triggers its `RequiresReplace` plan. If that listing cannot report
  the type, the import fails with a diagnostic instead of silently planning a replacement.
  `config_json` still has to be supplied by the configuration, because the server redacts backend
  credentials from that listing; the resulting plan updates the tier in place instead of deleting
  and recreating it.

SECURITY:
- The provider is built with Go 1.27.3, which clears GO-2026-6809 (compromised intermediate certificates in crypto/x509).

CHORES:
- Feature PRs no longer edit `CHANGELOG.md`, `AGENTS.md`, `README.md`, `docs/index.md` or
  `templates/`: a change adds a `changelog.d/<issue>-<kind>-<slug>.md` fragment, and
  `make changelog-release VERSION=x.y.z` renders it below `## Unreleased`. The resource and data
  source tables and the README counts are regenerated by `make generate-tables` from the provider
  registry and the generated docs, and the provider docs index no longer hardcodes counts.
- The Markdown conventions are now enforced on every pull request instead of being a written rule.
  CI fails when a change edits `CHANGELOG.md` outside a release branch, omits a
  `changelog.d/<issue>-<kind>-<slug>.md` fragment (unless the `skip-changelog` label is set), or
  touches `AGENTS.md`, `README.md`, `docs/index.md` or `templates/` without the `shared-md` label.
  The generated tables are checked for deterministic output rather than for being current, so a
  feature PR still adds only its own files. `docs/` drift is now reported on the pull request that
  causes it rather than on `main` after the merge.
- Releasing no longer edits shared files by hand. The new **Prepare Release** workflow bundles every
  `changelog.d/` fragment into a dated `CHANGELOG.md` section, regenerates the resource and data source
  tables in `AGENTS.md` and `README.md`, and pushes the result to a `release/v<version>` branch. It
  refuses to run when the branch would contain anything else, and the version is resolved by
  `scripts/next-version.sh`, which **Create Release Tag** now shares so the two workflows cannot
  disagree. Opening the pull request and creating the tag stay manual.
- The **Prepare Release** workflow now opens or updates the release pull request itself, using a
  short-lived GitHub App installation token instead of `GITHUB_TOKEN` so the `pull_request` checks
  run on it. The job summary links the pull request; when the `APP_ID` variable and
  `APP_PRIVATE_KEY` secret are absent (forks, or before the App exists) the branch is still pushed
  and the summary prints the manual compare link.

## 0.5.1 (2026-10-19)

BUG FIXES:
- `rustfs_pools` reads `GET /rustfs/admin/v3/pools/list`, the route RustFS 1.0.x serves, instead of the
  MinIO-era `list-pools`, and exposes each pool's command line — its identity in the admin API — as
  the pool name. The data source failed with `501 NotImplemented` against every supported server
  before, and would have reported empty names even if the route had answered
- `rustfs_users` decodes the `list-users` payload the server actually sends: an object keyed by access
  key (`{"alice":{"status":"enabled"}}`, `{}` when a bucket has no users) instead of a JSON array it
  never returns, and lists users sorted by access key so the attribute does not reshuffle between
  reads
- `rustfs_rebalance` starts a rebalance with `POST /rustfs/admin/v3/rebalance/start` (the registered
  method; `PUT` is answered with `501 NotImplemented`) and `Update` copies `RebalanceResourceModel`
  into the state instead of handing the framework a `tfsdk.Plan`, which failed the state write
- The `rustfs_tier` example and the generated docs show the configuration shape RustFS 1.0.x accepts:
  a top-level `type` inside `config_json` and camelCase credentials (`accessKey`/`secretKey`). The
  previous example left `type` out, which the server rejects with "Specified tier type is unsupported"
- `rustfs_group` could neither be updated nor destroyed once it had members: RustFS refuses
  `DeleteGroup` with `group is not empty`, so every update failed with "Could not reset group
  members" and Terraform could not destroy a populated group (it destroys the group before the
  users it references). Updates now apply a membership delta over `update-group-members` and
  deletes detach the members first. `rustfs_group.members` is no longer planned as unknown when the
  configuration omits it either — that path treated the missing set as "no members wanted",
  stripped the group and could end an apply with an unknown value in state
- A resource removed outside Terraform no longer fails the refresh for `rustfs_policy`,
  `rustfs_user`, `rustfs_serviceaccount` and `rustfs_user_policy_attachment`: it leaves state so
  the next plan recreates it. `rustfs_serviceaccount` deletion is idempotent (RustFS answers
  `service account not exist`), and `rustfs_user_policy_attachment` reads the attachment back from
  `user-info`, so a policy detached outside Terraform is detected instead of staying in state
- `rustfs_tier` retries an add, edit or remove while RustFS reports that the previous tier mutation
  has not finished reconciling (`TierEditFailed`, `XMinioAdminTierBackendInUse`) instead of failing
  the apply
- `rustfs_remote_target` now replaces the target when `region` or `path` changes. RustFS embeds the
  region in the target ARN and only applies `path` through an update group this client does not
  send, so both were silently ignored and the plan never converged. Deleting the resource is also
  idempotent now: deleting a replication configuration removes the remote targets it references, so
  the target deletion that follows reports "not found" and that is treated as success
- `rustfs_site_replication` sent in-place updates with the payload shape of the add route, so
  changing `endpoint`, `access_key`, `secret_key`, `skip_tls_verify` or `ca_cert_pem` failed with
  `invalid JSON: invalid type: map, expected a string`. `site-replication/edit` parses a single
  `PeerInfo` object whose endpoint field is `endpoint` (singular), unlike `site-replication/add`,
  which takes an array of sites with `endpoints`; `Update` now sends that shape (verified against
  RustFS 1.0.1 and 1.0.3)
- `rustfs_ldap_policy_attachment` read the optional `is_group` flag of an import ID from the wrong
  position: importing `'<user_or_group>/<policy>/true'` stored `<user_or_group>/<policy>` as the
  target and `true` as the policy name. The flag is now read from the last segment, which is the
  form the resource documentation and the acceptance test have always used
- A refresh no longer fails when an object was deleted outside Terraform. `rustfs_group`,
  `rustfs_user`, `rustfs_policy`, `rustfs_service_account`, `rustfs_ldap_service_account`,
  `rustfs_quota`, `rustfs_config`, `rustfs_tier`, `rustfs_kms_key`, `rustfs_remote_target`,
  `rustfs_bucket_durability`, `rustfs_bucket_encryption`, `rustfs_bucket_object_lock` and
  `rustfs_user_policy_attachment` now remove the resource from state when the server reports it
  missing (`resp.State.RemoveResource`), so the next plan offers to recreate it instead of failing
  the refresh. The missing signal is the structured admin/S3 error code (`NoSuchResource`,
  `NoSuchBucket`, `NoSuchConfiguration`, …) plus the endpoint's own wording where no code exists —
  never a blanket "not found" substring — and each one was probed against a running RustFS
- Destroying an object that is already gone is a no-op instead of a failure for the same resources
  plus `rustfs_site_replication`, `rustfs_bucket_notification` and `rustfs_ldap_policy_attachment`;
  `rustfs_remote_target` also treats a source bucket that no longer exists as already removed, next
  to the target-not-found case it already handled. RustFS reports an already-gone object in several
  shapes (404 `NoSuchResource`, 500 `InternalError` "failed to capture bucket incarnation", 400
  `InvalidRequest` "service account not exist"), so each resource matches the code and text its
  endpoint actually returns
- `rustfs_tier` actually reads the tier. `Read` used to re-write the incoming state unchanged, which
  meant a tier removed outside Terraform stayed in state forever; it now asks the server about that
  exact name (`GET tier/<name>`, which answers `TierNotFound` for a tier it does not hold on 1.0.1
  and latest) and drops the resource only on that positive answer. A server that does not implement
  the route keeps the last known state instead of planning a destructive recreation, and a list
  endpoint is deliberately not used because a list only proves absence when it is complete
- `rustfs_user_policy_attachment`'s `Read` reads the user back instead of preserving the last known
  state, so a policy detached outside Terraform is detected and the attachment is planned again
  rather than left dangling in state
- `rustfs_bucket_durability`'s `Read` no longer writes an empty `mode` into a required attribute: a
  bucket without a durability override — which is also what the endpoint answers for a bucket that no
  longer exists — removes the resource from state
- `rustfs_config` matches the admin API's structured `InvalidRequest` code and the endpoint's own
  wording (`config subsystem '…'`, `config target '…'`, `unsupported config subsystem '…'`) for a
  scope that is not configured, instead of any error text containing "not found". A successful body
  with no rendered keys no longer removes the resource: an unset subsystem answers with its
  defaults, so an empty body is not proof that the scope is gone
- `rustfs_notification_target`'s destroy no longer swallows any error whose text contains "404".
  RustFS answers 200 for a reset of a target that is already gone, so the substring matcher could
  only ever hide an unrelated failure
- `rustfs_user` and `rustfs_serviceaccount` mark `secret_key` as `Sensitive`, so the plaintext secret
  is redacted from plan and apply output instead of being printed with the rest of the plan
- The KMS and service-account missing-object matchers only fire on the endpoint's own answer: a
  missing KMS key is a 404 or the `Key not found` message the endpoint renders into a 200 body, so an
  unrelated failure such as `503 kms service is not running` is still reported, and the
  service-account read accepts the delete endpoint's `InvalidRequest "service account not exist"`
  shape as well as `NoSuchResource`
- `rustfs_bucket_encryption`, `rustfs_bucket_object_lock` and `rustfs_site_replication` report the
  upstream error text in the diagnostic detail (for example `Could not remove bucket encryption: `)
  instead of only the error string, and `rustfs_site_replication`'s destroy treats a site that is not
  configured as already removed

IMPROVEMENTS:
- The acceptance suite changes resources now instead of only creating and destroying them. Every
  resource test drives the in-place update with the new value read back from the server and a
  revert, an explicit empty second plan (the "Provider produced inconsistent result after apply"
  class of bug), a replacement plan for each `RequiresReplace()` attribute that is then applied for
  real, `ImportState` with `ImportStateVerify` — plus the malformed-ID diagnostic where the import
  ID is composite — and out-of-band drift followed by the converging step. Merged unit + acceptance
  statement coverage rose from 72.3% to 83.5%, and the floor in `.github/coverage-baseline.txt` moved
  with it (`floor_total` 71.8 → 83.0), so the 17 `Update` and 11 `ImportState` functions that read
  0% in issue #75 are covered
- The eight types that had no test — `rustfs_tier`, `rustfs_rebalance`, `rustfs_iam_backup_import`,
  `rustfs_bucket_metadata_backup_import`, `rustfs_pools`, `rustfs_users`, `rustfs_iam_backup` and
  `rustfs_bucket_metadata_backup` — now have schema, metadata, configure and CRUD units driven
  against a mocked admin API, which covers their failure paths too (invalid JSON, undecodable
  base64, server errors), plus acceptance tests against a live server: the import resources import a
  real archive exported from it, the pools and users data sources assert their shape, and
  `rustfs_tier` covers its import round trip. The acceptance compose file runs a second RustFS so a
  tier has a backend the server can probe (`RUSTFS_TIER_ENDPOINT`, plus `RUSTFS_TIER_S3_ENDPOINT`
  for host-based runs)
- `rustfs_rebalance`'s acceptance test skips itself unless the deployment has more than one pool: a
  single-pool deployment has nothing to rebalance and the route answers `501 NotImplemented`, which
  is what every supported server in CI reports
- Statement coverage is enforced and published. `scripts/coverage-gate.sh` fails the new `Coverage`
  check when the merged unit + acceptance coverage drops below
  `.github/coverage-baseline.txt`, and the `latest` cell of the acceptance matrix repeats that gate
  on `main`, so the floor can only move up. The README carries a generated coverage badge and a
  per-package bar chart (`scripts/coverage-readme.sh`); CI fails when they drift from the baseline.
  Merged coverage moved from 72.3% at 0.5.0 to 81.4% with the LDAP and site-replication tests, to
  88.3% with the read/delete/create error-path units, and to 90.2% with the update/import contracts
  and the remaining lifecycle units; both the 80% (Silver) and 90% (Gold) milestones are met
- The acceptance fixture provisions the prerequisites the bucket replication, bucket notification
  and remote target resources need, so their `TestAcc` tests run in CI instead of skipping:
  `rustfs-peer` is a second RustFS instance registered as a replication target, and the bucket
  notification test registers the `notify_webhook` target its configuration references. Both tests
  now cover create, update, import and destroy, and the remote target test covers an in-place
  credential update as well as a region replacement
- `pkg/rustfs` statement coverage rises from 88.5% to 97.8%: the live-only CRUD tests (user
  accounts, quotas, service accounts, storage tiers, canned-policy deletion) are mirrored with
  `httptest` mocks, so those paths run in CI instead of only where a developer has a server, and
  every admin call now has a unit test for its error, decode and edge branches. `DeletQuota` also
  drops a dead error branch that could never be taken. The 27 statements still uncovered are
  defensive `json.Marshal`, SigV4 signer and already-validated-JSON error returns that no input
  can reach
- `TestAccSiteReplicationResource` runs in the acceptance fixture instead of skipping:
  `RUSTFS_SITE_REPLICATION_PEER_ENDPOINT` gives the second site as RustFS sees it, the test waits
  for the peer to report ready, exercises an in-place update and keeps a verified import and
  destroy. A host-side run against a published port cannot form the group (RustFS refuses a
  loopback site endpoint) and skips with the server's own explanation

CHORES:
- Every resource's `Read`, `Create`, `Delete` and `Configure` error and idempotency paths are covered
  by unit tests that need no server: `provider/lifecycle_test_harness_test.go` builds on the mocked
  admin API primitives in `provider_test.go` (`newUnitAllClient`, `resourceSchemaOf`,
  `resourcePlan`/`resourceState`) and adds the S3 half of the client, per-request routing and the
  assertions a lifecycle case needs. Each resource's `_test.go` drives it through a table of canned
  responses (`read:` / `delete:` / `create:` / `configure:` subtests), asserting the diagnostic
  summary *and* detail, the removal from state, and that a failed create wrote no partial state.
  `AGENTS.md` documents the pattern. Provider unit coverage rises from 25.3% to 67.6% and the merged
  unit + acceptance figure from 81.4% to 88.3%
- The LDAP resources are covered against a mocked admin API. RustFS 1.0.x cannot be given an LDAP
  identity provider (`help-config-kv` has no `identity_ldap` sub-system, there is no `RUSTFS_*LDAP*`
  environment variable and `add-user` rejects the distinguished names the LDAP routes expect), so
  `TestAccLdapServiceAccount` and `TestAccLdapPolicyAttachment_*` skip on every supported version;
  their create/read/update/delete/import flows, including every documented import ID form, are
  asserted in `provider/` instead, which also pins the wire bodies those resources send

## 0.5.0 (2026-10-18)

BUG FIXES:
- `rustfs_group_policy_attachment` no longer replaces every policy on the group. Attach and detach
  go through the additive `POST /idp/builtin/policy/attach` and `/detach` endpoints (as
  `rustfs_user_policy_attachment` already did) instead of `PUT set-user-or-group-policy`, so a
  second attachment on the same group and policies attached outside Terraform survive a create and
  a destroy. Both calls are serialized because the endpoints read-modify-write the group's policy
  list, and `Read` now detects an out-of-band detach instead of leaving a dangling attachment in
  state
- The `insecure` provider attribute now really disables TLS certificate verification. The schema
  accepted it and the docs described it as "skip SSL validation", but the value never reached the
  transport, so a self-signed server failed with `x509: certificate signed by unknown authority`.
  Both API clients now share one `*http.Client`, which also means the admin API finally honours the
  provider's proxy settings, timeouts and TLS minimum (it used a bare `http.Client` before)

FEATURES:
- `rustfs_policy` statements gain `sid`, `not_action`, `not_resource` and `condition`, so
  prefix-scoped and negative statements (`condition = { StringLike = { "s3:prefix" = ["tenant-42/*"] } }`)
  can be applied through the provider. `action` is now optional: a statement must set `action` or
  `not_action`, and condition values returned as strings, booleans or numbers are normalized to
  lists of strings so the state does not diff
- The provider docs publish the supported RustFS version matrix: the README and the provider index
  list every capability against RustFS `1.0.1`, `1.0.2` and `1.0.3`, and explain why a route that
  exists in the server's route table is not proof of support (RustFS answers `501 NotImplemented`
  for routes it registered but has not implemented)
- The provider now detects the server version (`GET /rustfs/admin/v3/healthinfo`) and maps `404`,
  `405` and `501` on a version-gated route to an `UnsupportedFeatureError` naming the feature, the
  route and the server version

IMPROVEMENTS:
- `rustfs_metrics` reports an unimplemented `GET /rustfs/admin/v3/metrics` route as an unsupported
  capability naming the server version and the route, instead of surfacing the raw S3 XML payload.
  RustFS 1.0.1 through 1.0.3 register the route but do not implement it, and `TestAccMetricsDataSource`
  now skips only on that capability gap instead of on any error
- CI runs the acceptance suite against RustFS `1.0.1`, `1.0.2`, `1.0.3` and `latest`, and
  `TestAccServerVersion_supported` records the server version and metrics availability for every
  server the suite runs against

CHORES:
- The parsers that consume untrusted input are fuzzed: `FuzzPolicyDocument`, `FuzzAdminErrorBody`
  and `FuzzConfigKV` in `pkg/rustfs` and `FuzzTargetConfig` in `provider`, backed by a committed
  seed corpus in `testdata/fuzz/` and a weekly `Fuzz` workflow (one minute per target). `make fuzz`
  runs the same targets locally
- The release workflow attaches SLSA v1.0 build provenance to every release: it attests the
  artifacts listed in `terraform-provider-rustfs_<version>_SHA256SUMS` with
  `actions/attest-build-provenance` (`id-token: write` and `attestations: write` on the release
  job only) and publishes the Sigstore bundle as
  `terraform-provider-rustfs_<version>_provenance.intoto.jsonl`, so `gh attestation verify` works
  for a downloaded artifact — against the attestation record or, with `--bundle`, from the release
  assets alone
- The acceptance suite is instrumented for coverage (`-covermode=atomic -coverpkg=./...`, profile
  written to `coverage-acceptance.out`), `scripts/merge-coverage.sh` merges it with the unit
  profile, and the acceptance job publishes the combined number in its job summary. `make
  coverage-merge` does the same locally
- The acceptance container downloads the Terraform build for its own architecture
  (`dpkg --print-architecture`), so the suite runs on Apple Silicon (linux/arm64) instead of
  failing to execute a linux_amd64 binary
- Licence references are consistent again: the repository is Apache-2.0 (`LICENSE`, GitHub,
  `weinmann-emt/terraform-provider-rustfs` and `terraform-provider-gravitino` all agree), the two
  `MPL-2.0` file headers left over from the HashiCorp provider scaffolding and the `MPL-2.0` lines
  in `README.md`/`CONTRIBUTING.md` are gone, every Go file carries a copyright and SPDX header, and
  `NOTICE` records the upstream projects
- Added `GOVERNANCE.md`, `ROADMAP.md`, `ARCHITECTURE.md`, and `.github/OPENSSF-BEST-PRACTICES.md`
  (the evidence pack for the OpenSSF Best Practices badge); `SECURITY.md` now documents the security
  requirements, the threat model and the assurance case, and `CONTRIBUTING.md` documents the code
  review and testing requirements
- Cleared the reachable OSV advisories: `golang.org/x/net` v0.61.0 (HTTP/2 framing, trailer and
  flow-control fixes) and `github.com/yuin/goldmark` v1.8.6 (light/dark auto-link XSS), plus the
  transitive `golang.org/x` bumps that `go mod tidy` selected; `docs/` regenerates identically
- Go toolchain bumped to 1.27.2, which carries the matching `net/http` HTTP/2 stdlib fixes
- CI now runs `govulncheck ./...` (`Vulnerability Scan` job), and the unfixable
  `golang.org/x/crypto/openpgp` advisory is documented as accepted in
  [`.github/SECURITY.md`](./.github/SECURITY.md)

## 0.4.1 (2026-10-18)

CHORES:
- The acceptance environment no longer pins the notify and audit feature modules with
  `RUSTFS_NOTIFY_ENABLE`/`RUSTFS_AUDIT_ENABLE`: RustFS rejects every admin API update to an
  env-pinned module, which broke `TestAccModuleSwitchResource`. The audit and notification
  target tests now enable their own module through the admin API and restore it afterwards, so
  the module switch resource is testable again
- `TestAccStorageInfo` no longer mirrors the volatile `raw_json` of its live disk metrics
  (`avail_space`, `free_inodes`, `used_space`) into an output, which made the second step flap
  between an empty and a non-empty refresh plan depending on whether the metrics had moved

## 0.4.0 (2026-10-18)

FEATURES:
- **New Resource:** `rustfs_notification_target` — event-notification targets for all nine target
  types (`notify_webhook`, `notify_amqp`, `notify_kafka`, `notify_mqtt`, `notify_nats`,
  `notify_postgres`, `notify_mysql`, `notify_redis`, `notify_pulsar`), configured through
  mutually-exclusive typed `*_config` blocks, with `enable`, `comment` and the computed
  `status`/`health_state`/`health_reason` health metadata
- `rustfs_audit_target` gains the same nine `*_config` blocks plus `enable`, so audit targets can
  be configured for every target type; the flat webhook attributes keep working and cannot be
  combined with a typed block

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
