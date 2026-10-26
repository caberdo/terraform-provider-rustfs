# Roadmap

What this project intends to do — and deliberately not do — over the next twelve months. It is
maintained in the open: the living version of the near-term work is the
[issue tracker](https://github.com/caberdo/terraform-provider-rustfs/issues), and this file records
the direction those issues add up to. It is reviewed once per release (`CHANGELOG.md` records what
actually shipped).

## What the project is

A Terraform provider for [RustFS](https://github.com/rustfs/rustfs): it manages S3 API objects
(buckets, policies, tags, CORS, versioning, lifecycle, replication, encryption) and RustFS admin API
v3 objects (IAM users/groups/policies, service accounts, LDAP, KMS keys, quotas, storage tiers and
ILM, audit/notification targets, cluster config and health) through Terraform state. The complete
resource and data source list is in the README; the provider is tested against RustFS `1.0.1`,
`1.0.2`, `1.0.3` and `latest`.

## Landed — supply-chain and process hardening

The supply-chain cycle is complete. Every GitHub Action is pinned to a commit SHA, the parsers are
fuzzed with a committed seed corpus and a weekly `Fuzz` workflow, releases carry SLSA v1.0 build
provenance next to the GPG-signed checksums, and `rustfs_policy` supports `Sid`, `Condition`,
`NotAction` and `NotResource`. The OSV advisories were cleared with `govulncheck` in CI, branch
protection and the OpenSSF Best Practices badge were put in place, and the scorecard records the
result. These are maintained, not pending; regressions belong in the issue tracker.

## Now — provider coverage and correctness

The current cycle closes the remaining distance between the provider and what RustFS serves. The
work is tracked in epic [#146](https://github.com/caberdo/terraform-provider-rustfs/issues/146) and
its lane epics; the directions are:

* **S3 bucket surfaces** — model bucket website, logging, accelerate and request-payment settings,
  make the bucket ACL updatable, and add the policy-status read surface.
* **Read parity** — data sources next to existing resources: buckets, storage tiers, KMS keys and
  service accounts.
* **Object data plane** — decide whether `rustfs_object` belongs in the provider at all, or is a
  documented non-goal. The decision comes before any implementation.
* **Heal, resync and MRF** — decide whether heal belongs in the provider, then expose
  site-replication status, metainfo and resync as the decision allows. MRF counters are read-only
  today; queue actions are declined in the non-goals table.
* **Remaining admin jobs** — config-history clearing, quota statistics and quota checks, and the
  read surfaces those routes need.
* **Developer experience** — resolve the endpoint scheme once so both clients reach the same
  origin, and add `force_destroy` to `rustfs_bucket`.
* **Version matrix** — extend `.github/rustfs-versions.txt` when a new RustFS release implements an
  admin route the provider already maps.

Statement coverage is gated on every pull request. The current merged measurement is 90.2% total
(`pkg/rustfs` 97.8%, `provider` 88.0%) against a floor of 89.5%, 97.3% and 87.5% respectively;
`.github/coverage-baseline.txt` is the source of truth and `README.md` carries the generated table.
The 90% milestone is met; the floor moves only with a fresh unit-plus-acceptance measurement.

## Later — structure and reach

* **Keep the flat layout.** `pkg/rustfs` and the flat `provider` package are the settled structure.
  The `internal/` move proposed in
  [#2](https://github.com/caberdo/terraform-provider-rustfs/issues/2) was dropped; a future move
  needs its own accepted issue, and Terraform type names remain public API either way.
* **Discoverability**: keep the provider listed in the OpenTofu registry and the relevant
  awesome-lists, and keep the RustFS documentation cross-links current.
* **Release cadence**: patch releases for RustFS compatibility and bug fixes, minor releases for new
  resources and data sources, deprecations announced one minor release ahead of removal.

## Explicit non-goals

* **Not a RustFS server, console or deployment tool.** The provider configures a running RustFS
  instance; it does not provision clusters, drives or containers — use the RustFS installers,
  Ansible, Kubernetes or the RustFS console for that.
* **Not a MinIO provider or a generic S3 provider.** The S3 surface here exists because RustFS
  implements it; S3-compatible systems with different admin APIs are out of scope.
* **No breaking schema changes.** Renaming or removing a Terraform type or attribute breaks user
  state; such changes only happen with a documented upgrade path and a major version.
* **No hosted service or paid tier.** This repository is the whole product.
* **No roadmap dates.** Work is ordered by value, not by calendar; the issue labels and this file
  are the commitment.

## How to influence this

Open an issue using the templates in `.github/ISSUE_TEMPLATE/`, or comment on an existing one.
Decisions and their reasoning are recorded in the issue before the code lands — see
[`GOVERNANCE.md`](./GOVERNANCE.md).
