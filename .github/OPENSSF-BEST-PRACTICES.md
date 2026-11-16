# OpenSSF Best Practices badge — evidence pack

Everything needed to register `caberdo/terraform-provider-rustfs` at
[bestpractices.dev](https://www.bestpractices.dev), answer the questionnaire, publish the badge and
make OpenSSF Scorecard's `CII-Best-Practices` check stop reading 0.

Prepared 2026-10-18 against repository revision `a78fe7f` plus this change, BadgeApp criteria from
[`criteria/criteria.yml`](https://github.com/coreinfrastructure/best-practices-badge/blob/main/criteria/criteria.yml)
and Scorecard v5 policy as linked below. Answers are grouped by badge level; only **MUST** criteria
decide whether a level is awarded.

## 1. How Scorecard scores this check — read this first

| Badge level | Scorecard score |
|---|---|
| In Progress | 2 |
| **Passing** | **5** |
| **Silver** | **7** |
| **Gold** | **10** |

Source: [`checks/evaluation/cii_best_practices.go`](https://github.com/ossf/scorecard/blob/main/checks/evaluation/cii_best_practices.go)
(`passingScore = 5`, `silverScore = 7`, `gold` = max) and
[`probes/hasOpenSSFBadge/impl.go`](https://github.com/ossf/scorecard/blob/main/probes/hasOpenSSFBadge/impl.go)
for the level names.

**This corrects the acceptance criterion in [#53](https://github.com/caberdo/terraform-provider-rustfs/issues/53).**
"Scorecard `CII-Best-Practices` = 10" requires a **Gold** badge, not "start with passing" (which
scores 5). Scorecard reads the level from bestpractices.dev, not from the README:

```
GET https://www.bestpractices.dev/projects.json?url=https://github.com/caberdo/terraform-provider-rustfs
→ [{"badge_level": "passing", …}]        # empty today: the project is not registered
```

Source: [`clients/cii_http_client.go`](https://github.com/ossf/scorecard/blob/main/clients/cii_http_client.go)
and [`clients/cii_response.go`](https://github.com/ossf/scorecard/blob/main/clients/cii_response.go).
The README badge is for humans (and it is what §3 adds) — Scorecard itself does not parse it.

**Where that leaves the climb:** Passing (5) is reachable now; Silver (7) needs ~80% statement
coverage, which is met at 90.2% today (§5); Gold (10) is **not reachable while the project
has one maintainer**, because `bus_factor`, `contributors_unassociated` and `two_person_review` are
MUST criteria at that level and none of them can be satisfied by writing code (§5). The pack
therefore pre-answers all three levels so the project can claim each one that its people and
coverage justify, instead of discovering that after submission.

## 2. Register the project (maintainer, ~10 minutes, cannot be delegated)

1. Open <https://www.bestpractices.dev> and sign in with GitHub (the identity is what the badge
   links to).
2. **Get started** → *Add a new project*:
   * **Project name:** `Terraform Provider for RustFS`
   * **Project homepage URL:** `https://github.com/caberdo/terraform-provider-rustfs`
   * **Source code repository:** `https://github.com/caberdo/terraform-provider-rustfs`
   * **Project description:** what `README.md` says — a Terraform provider that manages RustFS
     (S3-compatible object storage) as code, over the S3 API and the RustFS admin API v3.
   * **License:** `Apache-2.0` (the SPDX identifier; `LICENSE` and GitHub agree).
3. Answer the questionnaire from §4. The form saves as you go; criteria answered `Met` need a
   justification and, where the table says so, a URL.
4. Submit at **Passing** first (`badge_level: passing` → Scorecard 5). Silver needs the coverage
   and continuity items in §5; Gold needs a second maintainer.
5. Paste the badge from §3 into the README and merge it.

## 3. The README badge (after step 2, and after §5's blockers)

`bestpractices.dev` assigns a numeric project id. Add this line to the badge block at the top of
`README.md` (right after the CI badge, before the Scorecard badge), replacing `PROJECT_ID` with the
id from the project URL `https://www.bestpractices.dev/projects/PROJECT_ID`:

```markdown
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/PROJECT_ID/badge)](https://www.bestpractices.dev/projects/PROJECT_ID)
```

Notes:

* The id does not exist until the project is registered, so this line cannot be committed before
  step 2 — it is the only part of this pack that is deliberately left as a fill-in.
* `.github/github-action-markdown-link-check-config.json` already ignores
  `bestpractices.dev/projects` URLs, so the badge cannot fail the `Validate Markdown Files` check
  while the project does not exist yet (and a `404` badge image never fails a link check anyway).
* Contract alignment: this also satisfies the Silver criterion `documentation_achievements`, which
  requires the achievement to be linked from the repository front page.

## 4. Answers to paste into the questionnaire

`Evidence` column: `blob/…` entries are
`https://github.com/caberdo/terraform-provider-rustfs/blob/main/…`.

### 4.1 Passing (level 0) — required criteria

| Criterion | Answer | Justification | Evidence |
|---|---|---|---|
| `description_good` | Met | The README and the repository description both say what the provider does: manage RustFS (S3-compatible object storage) as code, covering the S3 API and the admin API v3, with the current count of types. | `blob/README.md` |
| `interact` | Met | The README's Quick start shows how to obtain the provider (`required_providers` + `terraform init`, Terraform Registry and OpenTofu links), how to give feedback (issue templates, `SUPPORT.md`) and how to contribute (`CONTRIBUTING.md`). | `blob/README.md`, `blob/SUPPORT.md` |
| `contribution` | Met | `CONTRIBUTING.md` explains the process: fork, one change per pull request, required checks, review, `make fmt vet lint test`, `make generate`. | `blob/.github/CONTRIBUTING.md` |
| `floss_license` | Met | Apache-2.0, an OSI-approved licence; `LICENSE` is the Apache License 2.0 text and GitHub reports `Apache-2.0`. | `blob/LICENSE` |
| `license_location` | Met | The licence is in the standard repository location and referenced from the README. | `blob/LICENSE` |
| `documentation_basics` | Met | README (install, quick start, provider configuration, requirements), `examples/` (a full configuration plus one per resource/data source), generated reference in `docs/`, and a security best-practices section. | `blob/README.md`, `blob/examples/complete`, `blob/docs/index.md` |
| `documentation_interface` | Met | Every resource and data source has generated reference documentation listing every attribute, its type, whether it is required/optional/computed, and import instructions. | `blob/docs/resources`, `blob/docs/data-sources` |
| `sites_https` | Met | All project sites are HTTPS: GitHub repository, GitHub Releases, Terraform Registry, OpenTofu registry and the links from the README. | `https://github.com/caberdo/terraform-provider-rustfs` |
| `discussion` | Met | GitHub Issues and pull request discussions: searchable, each thread has a URL, anyone with a GitHub account can take part, no proprietary client required. | `https://github.com/caberdo/terraform-provider-rustfs/issues` |
| `maintained` | Met | Active maintenance: releases `v0.1.0`–`v0.4.1`, commits on `main` in the last days, weekly Scorecard run, Dependabot enabled daily. | `https://github.com/caberdo/terraform-provider-rustfs/releases` |
| `repo_public` | Met | Public Git repository with a URL. | `https://github.com/caberdo/terraform-provider-rustfs` |
| `repo_track` | Met | Git history records every change with author and timestamp; `main` is PR-only. | `blob/CHANGELOG.md` |
| `repo_interim` | Met | The repository carries interim versions between releases: every change is a pull request with reviewable commits, and `CHANGELOG.md` has an `Unreleased` section. | `https://github.com/caberdo/terraform-provider-rustfs/pulls?q=is%3Apr+is%3Aclosed` |
| `version_unique` | Met | Every release has a unique version identifier: Git tags `v0.1.0` … `v0.4.1`, matching the version embedded in the binary (`-X main.version`) and the registry manifest. | `https://github.com/caberdo/terraform-provider-rustfs/tags` |
| `release_notes` | Met | Each release has human-readable release notes in `CHANGELOG.md` (grouped into features/improvements/fixes/chores/breaking changes, not a raw commit log), and the GitHub release page links the pull requests behind it. | `blob/CHANGELOG.md` |
| `release_notes_vulns` | N/A | No publicly known vulnerability (CVE or equivalent) has ever been fixed in a release of this project, so there is nothing to list. | — |
| `report_process` | Met | Bug reports go through a GitHub issue form that asks for the provider, Terraform and RustFS versions, reproduction steps and the error output; `SUPPORT.md` points at it. | `blob/.github/ISSUE_TEMPLATE/bug_report.yml` |
| `report_responses` | Met | The project is 11 days old, so no report falls in the 2–12 month window; the reports filed so far were triaged (labels, linked issue) and the one feature request from outside the project was closed by the pull request that implemented it (#32 → #33). | `https://github.com/caberdo/terraform-provider-rustfs/issues?q=is%3Aissue` |
| `report_archive` | Met | All issues and pull requests, with their comments, stay public and searchable. | `https://github.com/caberdo/terraform-provider-rustfs/issues?q=is%3Aissue` |
| `vulnerability_report_process` | Met | `SECURITY.md` publishes the process: do not open a public issue, report privately, and what to include (type, versions, impact, reproduction, PoC). | `blob/.github/SECURITY.md` |
| `vulnerability_report_private` | Met | Private reports are supported through GitHub's private vulnerability reporting, with the exact link in `SECURITY.md`. | `blob/.github/SECURITY.md` |
| `vulnerability_report_response` | N/A | No vulnerability report has been received in the last 6 months; `SECURITY.md` commits to acknowledging within 48 hours. | `blob/.github/SECURITY.md` |
| `build` | Met | Any developer or CI can rebuild the provider from source: `go build ./...` or `make build` (Go toolchain version pinned in `go.mod`). | `blob/GNUmakefile` |
| `test` | Met | An automated test suite is released as FLOSS with the project (unit tests plus acceptance tests); how to run it is documented in `README.md`, `CONTRIBUTING.md` and the `make` targets. | `blob/GNUmakefile`, `blob/README.md#testing-and-coverage` |
| `test_policy` | Met | Written policy in `CONTRIBUTING.md` ("Testing policy"): major new functionality must come with tests, bug fixes with a regression test. | `blob/.github/CONTRIBUTING.md` |
| `tests_are_added` | Met | Recent changes carry tests: the acceptance tests for the audit/notification targets, the server-version capability tests and the regression tests added with the fixes in `v0.4.1`. | `https://github.com/caberdo/terraform-provider-rustfs/pulls?q=is%3Apr+is%3Aclosed` |
| `warnings` | Met | CI runs `go vet` and golangci-lint (staticcheck, errcheck, bodyclose, govet with nearly every check) with a pinned linter version, on every pull request. | `blob/.golangci.yml` |
| `warnings_fixed` | Met | Lint and vet findings block a merge: `ci / Lint & Security` is a required status check on `main`, and the gosec step fails the job when it reports findings. | `blob/.github/workflows/test.yml` |
| `know_secure_design` | Met | The design is documented with its security argument: saltzer/schroeder-style principles (economy of mechanism, fail-safe defaults, complete mediation, open design) mapped to the code, plus the trust boundaries. | `blob/.github/SECURITY.md`, `blob/ARCHITECTURE.md` |
| `know_common_errors` | Met | The assurance case lists the weakness classes relevant to this software (injection, XXE, insecure deserialization, sensitive data exposure, MITM, dependency risk) and how each is countered or accepted. | `blob/.github/SECURITY.md` |
| `crypto_published` | Met | The only cryptography is published and reviewed: TLS 1.2+ for transport, HMAC-SHA256 with SHA-256 digests for AWS SigV4 request signing. | `blob/pkg/rustfs/admin_client.go` |
| `crypto_floss` | Met | All cryptography comes from FLOSS: the Go standard library (`crypto/tls`, `crypto/sha256`) and the Apache-2.0 AWS SDK for Go v2 signer. | `blob/NOTICE`, `blob/go.mod` |
| `crypto_keylength` | Met | Key lengths are the Go defaults and meet NIST's through-2030 minimums: TLS 1.2/1.3 with ≥128-bit ciphers, HMAC-SHA256 (256-bit). The provider configures no weaker mechanism. | `blob/provider/provider.go` |
| `crypto_working` | Met | No broken algorithm or mode is used or configurable: no MD4/MD5, DES/3DES, RC4, no CBC-mode negotiation of its own, `MinVersion: tls.VersionTLS12`. | `blob/provider/provider.go` |
| `crypto_password_storage` | N/A | The provider authenticates *to* RustFS with the credentials you give it; it never stores passwords for authenticating external users (that is the RustFS server's job, not this project's software). | — |
| `crypto_random` | N/A | The provider generates no cryptographic keys and no nonces: credentials are supplied by the user and SigV4 needs only the request hash and a timestamp. | — |
| `delivery_mitm` | Met | Releases are downloaded over HTTPS/SSH from GitHub Releases and the Terraform Registry; Terraform records the checksums in `.terraform.lock.hcl`. | `blob/README.md#verifying-a-release` |
| `delivery_unsigned` | Met | Nothing is verified by an unsigned hash: `SHA256SUMS` is GPG-signed with the release key (published in the repository), the tag itself is GPG-signed and GitHub-verified, every artifact carries SLSA v1.0 build provenance (`gh attestation verify`), and Terraform checks the release checksums. | `blob/.github/release-signing-key.asc`, `blob/.github/workflows/release.yml` |
| `vulnerabilities_fixed_60_days` | Met (see §6, judgement call) | The two advisories older than 60 days are not reachable in released software: the goldmark advisory is confined to the docs-generation tooling (reachable only from `tools/` → `tfplugindocs`, not from the provider binary) and the `x/crypto/openpgp` advisory is for a package the main module does not need and that has no fixed version. Everything reachable was raised within 60 days and is tracked in [#49](https://github.com/caberdo/terraform-provider-rustfs/issues/49). | `https://github.com/caberdo/terraform-provider-rustfs/issues/49` |
| `no_leaked_credentials` | Met | No valid private credential is in the repository or its history; the only credential-shaped strings are AWS's documented `AKIAIOSFODNN7EXAMPLE` in examples. `.gitignore` excludes key material, secrets are supplied via the provider block or `RUSTFS_*` environment variables at runtime, and `secret_key`/`access_secret` are `Sensitive`. | `blob/.gitignore`, `blob/provider/provider.go` |
| `static_analysis` | Met | Static analysis runs before every release and on every pull request: gosec (security rules, SARIF uploaded to GitHub code scanning) and golangci-lint (staticcheck, govet, errcheck, bodyclose). | `blob/.github/workflows/test.yml`, `blob/.golangci.yml` |
| `static_analysis_fixed` | Met | Findings block the merge: the gosec step fails the job after uploading its SARIF, and `ci / Lint & Security` is a required status check; the one deliberate suppression (`#nosec G402` on the opt-in `insecure` attribute) is justified in place. | `blob/provider/provider.go`, `blob/.github/workflows/test.yml` |
| `dynamic_analysis_fixed` | Met | The dynamic analyses in use (the acceptance suite against a real RustFS server, plus `go test -race`) have reported no exploitable vulnerability, and any finding is fixed under the same policy as static findings. | `blob/acc_test/docker-compose.yml` |

### 4.2 Passing — SHOULD / SUGGESTED criteria (no level requires them; answer them anyway)

| Criterion | Answer | Justification |
|---|---|---|
| `contribution_requirements` | Met | `CONTRIBUTING.md` states the requirements: coding standards, tests, `make generate` for docs, one change per pull request. |
| `floss_license_osi` | Met | Apache-2.0 is an OSI-approved licence. |
| `english` | Met | All documentation, comments and commit messages are in English. |
| `report_tracker` | Met | GitHub Issues is the tracker for individual issues. |
| `enhancement_responses` | Met | Enhancement requests are answered in the issue and implemented through the linked pull request (e.g. the audit/notification target request in #32). |
| `build_common_tools` | Met | The build uses the standard Go toolchain; no custom build language is needed. |
| `build_floss_tools` | Met | The toolchain (`go build`, `make`, `goreleaser`, `terraform-plugin-docs`) is FLOSS. |
| `test_invocation` | Met | `go test ./...` or `make test`. |
| `test_most` | Met | Most tests run without special setup; the acceptance suite needs the shipped container environment (`make testacc-docker`). |
| `test_continuous_integration` | Met | `test.yml` runs on every pull request and push to `main`. |
| `tests_documented_added` | Met | The pull request template and `CONTRIBUTING.md` require tests for major new functionality. |
| `warnings_strict` | Met | `govet` runs with nearly all checks enabled, staticcheck and gofmt are enforced by the pinned golangci-lint configuration. |
| `crypto_call` | Met | Cryptography is used only through the Go standard library and the AWS SDK signer. |
| `crypto_weaknesses` | Met | No algorithm or mode with known serious weaknesses is used (SHA-256, TLS 1.2+). |
| `crypto_pfs` | Met | Go's TLS stack negotiates ephemeral (EC)DHE key exchange by default; the provider does not pin a non-PFS suite. |
| `vulnerabilities_critical_fixed` | Met | No open critical vulnerability; Dependabot and the Scorecard `Vulnerabilities` check watch the module graph. |
| `static_analysis_common_vulnerabilities` | Met | gosec's rule set targets common Go/CWE classes (G4xx), in addition to staticcheck's correctness rules. |
| `static_analysis_often` | Met | Both tools run on every pull request, far more often than once per six months. |
| `dynamic_analysis` | Met | The acceptance suite exercises every resource against a real RustFS server, `-race` builds provider and client under the race detector, and four fuzz targets replay their seed corpus on every run. |
| `dynamic_analysis_unsafe` | N/A | The provider is written in Go, which is memory-safe; no C/C++ is compiled (`CGO_ENABLED=0`). |
| `dynamic_analysis_enable_assertions` | Met | CI runs the unit suite with `-race`; test failures and panics fail the job. |
| `version_semver` | Met | Releases follow semantic versioning (`v0.1.0` … `v0.4.1`), with breaking changes called out in `CHANGELOG.md`. |
| `version_tags` | Met | Every release is tagged (`v0.4.1`), and the tag is GPG-signed and GitHub-verified. |
| `repo_distributed` | Met | Git. |

### 4.3 Silver (level 1) — required criteria

| Criterion | Answer | Justification | Evidence |
|---|---|---|---|
| `achieve_passing` | Met | The passing level is awarded (see §4.1). | `https://www.bestpractices.dev/projects/PROJECT_ID` |
| `contribution_requirements` | Met | `CONTRIBUTING.md` documents the coding standards, the testing policy, the docs regeneration step and the pull request process a change must follow. | `blob/.github/CONTRIBUTING.md` |
| `governance` | Met | `GOVERNANCE.md` documents the governance model: roles, how decisions are made (issue → pull request with required checks → maintainer decision, recorded in the issue), how releases are cut and how maintainers are added. | `blob/GOVERNANCE.md` |
| `code_of_conduct` | Met | The Contributor Covenant 2.1 is adopted as `CODE_OF_CONDUCT.md` in the standard location. | `blob/CODE_OF_CONDUCT.md` |
| `roles_responsibilities` | Met | `GOVERNANCE.md` names the roles (maintainer, organization owner, contributor, security reporter) and their responsibilities, including who holds the release key and registry namespace. | `blob/GOVERNANCE.md` |
| `access_continuity` | **Unmet — see §5** | The repository, registry namespace and release identity belong to the `caberdo` organization rather than a person, and the release process is documented, but there is currently one maintainer: a second person with organization access and access to the signing secrets is required before the project could release within a week if that person were lost. | `blob/GOVERNANCE.md#access-and-continuity` |
| `documentation_roadmap` | Met | `ROADMAP.md` states what the project intends to do next year, in order, and what it explicitly will not do (no server provisioning, no breaking schema changes, no dates). | `blob/ROADMAP.md` |
| `documentation_architecture` | Met | `ARCHITECTURE.md` documents the high-level design: entry point, provider wiring, the two API clients and their signing, the request flow, the error model, the repository layout, the test layers and the release pipeline. | `blob/ARCHITECTURE.md` |
| `documentation_security` | Met | `SECURITY.md` documents what users can and cannot expect: TLS verified by default, credentials never logged or written by the provider, signed requests, signed releases, and the limits (it does not make your configuration secure, protect a compromised server, or encrypt your state). | `blob/.github/SECURITY.md` |
| `documentation_quick_start` | Met | The README's Quick start applies a working configuration with the local Docker environment, and `examples/complete` is a full configuration. | `blob/README.md#quick-start` |
| `documentation_current` | Met | The documentation is generated and checked in CI: `docs/` is regenerated by `make generate` and the `Generate Docs` job fails on drift, examples are validated against the schema by `make validate-examples`, and the README/CONTRIBUTING were corrected in this change (stale Go version, structure, coverage numbers). | `blob/.github/workflows/test.yml` |
| `documentation_achievements` | Met once §3 lands | The badge is linked from the README as soon as it exists (that is exactly step §3); the repository front page also links the Terraform Registry, OpenTofu registry and Scorecard results. | `blob/README.md` |
| `sites_password_security` | N/A | No project site stores passwords for authenticating users: the sites are GitHub, the Terraform Registry and the repository itself. | — |
| `maintenance_or_update` | Met | The provider follows the latest release and documents the upgrade path: `terraform init -upgrade`, `CHANGELOG.md` with `BREAKING CHANGES` entries, `RequiresReplace()` on attributes that cannot change in place, and a migration guide from `weinmann-emt/rustfs`. | `blob/README.md#migrating-from-weinmann-emtrustfs` |
| `report_tracker` | Met | GitHub Issues. | `https://github.com/caberdo/terraform-provider-rustfs/issues` |
| `vulnerability_report_credit` | N/A | No vulnerability report has been resolved in the last 12 months; `SECURITY.md` documents the credit policy (credited by name unless anonymity is requested). | `blob/.github/SECURITY.md` |
| `vulnerability_response_process` | Met | `SECURITY.md` documents the response process and its timelines: acknowledge within 48 hours, assessment within 7 days, fix and advisory within 14 days. | `blob/.github/SECURITY.md` |
| `coding_standards` | Met | The coding standards are identified and required: `gofmt`, `go vet`, the repository's golangci-lint configuration, idiomatic Go, and the provider-specific conventions in `AGENTS.md`/`CONTRIBUTING.md`. | `blob/.github/CONTRIBUTING.md` |
| `coding_standards_enforced` | Met | Enforced automatically: golangci-lint runs with a pinned version as the required `ci / Lint & Security` check, and the configuration includes gofmt as a formatter. | `blob/.golangci.yml` |
| `build_standard_variables` | N/A | No native binaries are produced by a C toolchain: the build is pure Go with `CGO_ENABLED=0`, so `CC`/`CFLAGS`/`LDFLAGS` do not apply. | `blob/.github/goreleaser.yml` |
| `build_non_recursive` | N/A | A single Go module with no recursive make; there is no build system that recurses into subdirectories. | `blob/GNUmakefile` |
| `build_repeatable` | Met | The release build is parameterised for byte-identical results: `-trimpath`, `mod_timestamp` from the commit, `CGO_ENABLED=0`, no build-time network access. | `blob/.github/goreleaser.yml` |
| `installation_common` | Met | Users install it the standard way for a Terraform provider (`terraform init` from the Registry or the OpenTofu registry); developers use `make install` into `~/.terraform.d/plugins` for `dev_overrides`. | `blob/README.md#quick-start` |
| `installation_standard_variables` | N/A | There is no installer: the artifact is a provider plugin binary published through the Terraform Registry, which owns the install location. | — |
| `installation_development_quick` | Met | A developer gets a working environment with `git clone`, `go mod download` and `make test`; the acceptance environment is one `make testacc-docker` (or `make testacc-live`) away and needs no manual server setup. | `blob/.github/CONTRIBUTING.md` |
| `external_dependencies` | Met | All external dependencies are listed in a computer-processable way in `go.mod`/`go.sum`, and each release publishes an SPDX SBOM. | `blob/go.mod` |
| `dependency_monitoring` | Met | Dependabot checks `gomod`, `docker-compose` and `github-actions` daily and opens update pull requests; OpenSSF Scorecard's `Vulnerabilities` check reports OSV advisories weekly, and [#49](https://github.com/caberdo/terraform-provider-rustfs/issues/49) tracks adding `govulncheck` to CI. | `blob/.github/dependabot.yml` |
| `updateable_reused_components` | Met | Every reused component is a versioned Go module (`go.mod` + `go.sum`) with no vendored or convenient copies, so a vulnerable dependency is updated with `go get` and a Dependabot pull request. | `blob/go.mod` |
| `automated_integration_testing` | Met | Every pull request runs `go test -race` with a coverage report and the schema/metadata checks, and every push to `main` runs the acceptance matrix against real RustFS servers; both publish their result in the job summary. | `blob/.github/workflows/test.yml` |
| `regression_tests_added50` | Met | The recent bug fixes came with test work, and the written policy requires a regression test: the module-switch fix restored and extended `TestAccModuleSwitchResource`, the storage-info fix removed the volatile output that made the refresh plan flap, and the pull request template makes "regression test added" a review checklist item. | `https://github.com/caberdo/terraform-provider-rustfs/pulls?q=is%3Apr+is%3Aclosed` |
| `test_statement_coverage80` | **Met** | Merged unit + acceptance statement coverage is 90.2% (`pkg/rustfs` 97.8%, `provider` 88.0%), which meets the Silver threshold of 80%, and a gate fails any pull request that drops below the recorded floor — kept at 89.5%, above the threshold, so the criterion cannot be lost by a change that passes the gate ([#78](https://github.com/caberdo/terraform-provider-rustfs/issues/78) tracked the climb; [#79](https://github.com/caberdo/terraform-provider-rustfs/issues/79) took it past 90%). | `blob/README.md#testing-and-coverage` |
| `test_policy_mandated` | Met | Formal written policy in `CONTRIBUTING.md`: tests MUST be added for major new functionality, enforced by the pull request checklist. | `blob/.github/CONTRIBUTING.md` |
| `tests_documented_added` | Met | The pull request template and `CONTRIBUTING.md` state that tests are part of the change, with the exact commands to run. | `blob/.github/PULL_REQUEST_TEMPLATE.md` |
| `warnings_strict` | Met | Nearly all `go vet` checks are enabled (only `fieldalignment` and `shadow` are disabled, with the reason in the config), staticcheck's full default set runs, and gofmt is enforced. | `blob/.golangci.yml` |
| `implement_secure_design` | Met | The assurance case maps the design principles to the implementation (single mediated request path, fail-safe defaults, allow-listed inputs, no code execution) and the two known limitations are documented rather than hidden. | `blob/.github/SECURITY.md` |
| `crypto_weaknesses` | Met | SHA-256 for digests and HMAC, TLS 1.2 minimum; no SHA-1, no CBC-mode SSH, no weak ciphers selected by the provider. | `blob/provider/provider.go` |
| `crypto_credential_agility` | Met | Credentials are runtime inputs (provider block or `RUSTFS_*` environment variables) and are never compiled in, so they can be rotated without rebuilding anything; the admin API endpoint and TLS material are configured the same way. | `blob/provider/helper.go` |
| `crypto_certificate_verification` | Met | Verification is on by default: the transport verifies against the system roots (plus `SSL_CERT_FILE` if set) and requires TLS 1.2; verification is skipped only if `insecure = true` is configured explicitly. | `blob/provider/provider.go` |
| `crypto_verification_private` | Met | Go's TLS stack completes the handshake, including certificate verification, before any HTTP header is sent; the provider adds no pre-TLS request path. | `blob/provider/provider.go` |
| `signed_releases` | Met | Releases are cryptographically signed and the verification steps are documented: GPG-signed, GitHub-verified tags, GPG-signed `SHA256SUMS`, the public key committed in the repository, and the private key exists only as an encrypted organization secret used by the release workflow — it is never committed and never uploaded with the artifacts. | `blob/README.md#verifying-a-release` |
| `input_validation` | Met | Inputs are typed and allow-listed: the framework decodes plan/state into concrete Go types, 31 attributes validate their value against an explicit `OneOf` allow-list, numeric attributes are bounded (`AtLeastOneOf`, `Between`), and anything the server rejects is surfaced verbatim instead of being coerced. Free-form names are validated by the RustFS server (see §6 for the path-escaping caveat). | `blob/provider` |
| `hardening` | Met | Documented hardening: no shell/SQL/template evaluation, typed decoders, request signing with a body hash, TLS 1.2 minimum with verification by default, `-trimpath`/`CGO_ENABLED=0` release builds, gosec + staticcheck + Scorecard in CI, and the two accepted limitations written down. | `blob/.github/SECURITY.md` |
| `assurance_case` | Met | `SECURITY.md` provides the assurance case: the threat model with the five trust boundaries, an argument that secure design principles are applied, a table matching common implementation weaknesses to their countermeasures, the hardening in place and the accepted limitations. | `blob/.github/SECURITY.md#assurance-case` |
| `static_analysis_common_vulnerabilities` | Met | gosec's rule set is vulnerability-focused for Go (G101 secrets, G107 URL injection, G402-404 TLS, G304 file inclusion, …) and runs on every change; the SARIF goes to GitHub code scanning. | `blob/.github/workflows/test.yml` |
| `dynamic_analysis_unsafe` | N/A | The provider and client are Go, so no memory-unsafe code is produced; `CGO_ENABLED=0` keeps C out of the binary. | `blob/.github/goreleaser.yml` |
| `dco` (SHOULD) | N/A | Not required; contributions are licensed under Apache-2.0 by the contribution terms in `CONTRIBUTING.md`. | `blob/.github/CONTRIBUTING.md` |
| `bus_factor` (SHOULD, required at Gold) | Unmet | One maintainer today — see §5. | `blob/GOVERNANCE.md` |
| `accessibility_best_practices` (SHOULD) | N/A | The project produces no graphical user interface; the documentation is plain Markdown readable by assistive technology. | — |
| `internationalization` (SHOULD) | N/A | A CLI/library context: error strings and documentation are English-only by convention, which the `english` criterion expects. | — |

### 4.4 Gold (level 2) — required criteria

Gold is **not reachable today** (see §5): three of its MUST criteria are about having a second,
unassociated contributor, which no amount of code can substitute for. The answers below are ready
so the level can be claimed the moment the people-side items are true.

| Criterion | Answer | Justification | Evidence |
|---|---|---|---|
| `achieve_silver` | Met once Silver is awarded | Passing and Silver answers are complete in §4.1/§4.3. | `https://www.bestpractices.dev/projects/PROJECT_ID` |
| `bus_factor` | **Unmet** | One maintainer. A second person with repository/organization access and the release secrets is required. | `blob/GOVERNANCE.md#access-and-continuity` |
| `contributors_unassociated` | **Unmet** | All significant contributions to date come from one organization (`caberdo`). An unassociated contributor is needed. | `https://github.com/caberdo/terraform-provider-rustfs/graphs/contributors` |
| `copyright_per_file` | Met | Every Go source file carries a copyright statement naming the holder ("The terraform-provider-rustfs contributors"), and `NOTICE` records the upstream projects. | `blob/NOTICE` |
| `license_per_file` | Met | Every Go source file carries `SPDX-License-Identifier: Apache-2.0` in its header. | `blob/provider/provider.go` |
| `repo_distributed` | Met | Git. | `https://github.com/caberdo/terraform-provider-rustfs` |
| `small_tasks` | **Unmet — see §5** | The `good first issue` label exists, but no open issue currently carries it; identifying small tasks requires (re)labelling issues that a newcomer can finish, which is the one part of this criterion that only the maintainer can do. | `https://github.com/caberdo/terraform-provider-rustfs/labels` |
| `require_2FA` | Verify in the organization settings | Two-factor authentication must be enforced for everyone with write or admin access to `caberdo/terraform-provider-rustfs`, which is an organization-level setting (`Organization settings → Authentication security`); record the check here once confirmed. | `https://github.com/organizations/caberdo/settings/security` |
| `code_review_standards` | Met | `CONTRIBUTING.md` documents the review requirements: what is reviewed (correctness and error paths, tests, schema/state compatibility, docs/examples, security, RustFS compatibility), by whom, and what blocks a merge (required checks, resolved threads). | `blob/.github/CONTRIBUTING.md#code-review` |
| `two_person_review` | **Unmet** | Every pull request is currently merged by its author, because the `protect-main` ruleset deliberately requires zero approvals rather than blocking a single-maintainer project; the decision and its alternatives are recorded in [#54](https://github.com/caberdo/terraform-provider-rustfs/issues/54). | `blob/GOVERNANCE.md` |
| `build_reproducible` | Met | The release build is reproducible from the tagged revision: `go.mod`/`go.sum` pin the whole module graph, `-trimpath` removes checkout paths, `mod_timestamp` is the commit timestamp and `CGO_ENABLED=0` removes toolchain variance. | `blob/.github/goreleaser.yml` |
| `test_invocation` | Met | The suite is invocable in the standard way for Go: `go test ./...`, or `make test` / `make testacc-docker` for the documented split. | `blob/GNUmakefile` |
| `test_continuous_integration` | Met | `test.yml` integrates every pull request into `main` after lint, vet, unit tests, example validation and (on `main`) the acceptance matrix, and the checks are required to merge. | `blob/.github/workflows/test.yml` |
| `test_statement_coverage90` | **Met** | Merged unit + acceptance statement coverage is 90.2% (`pkg/rustfs` 97.8%, `provider` 88.0%): 4609 of 5110 statements ([#79](https://github.com/caberdo/terraform-provider-rustfs/issues/79)). The gate now enforces a 89.5% floor, and the statements that remain uncovered are enumerated as deliberate in §5.1. | `blob/README.md#testing-and-coverage` |
| `test_branch_coverage80` | N/A (see §6) | Go's coverage tooling measures statement coverage only; no mature FLOSS branch-coverage tool exists for Go, so this cannot be measured with the standard toolchain. | — |
| `crypto_used_network` | Met | Every network path uses TLS 1.2 or newer when the endpoint is configured for it (`ssl = true`) and certificate verification is on; the provider never downgrades a connection, and the plaintext admin port is used only when the user's endpoint says so (the endpoint is mandatory — the provider refuses to run without one). | `blob/provider/provider.go` |
| `crypto_tls12` | Met | `MinVersion: tls.VersionTLS12` on the shared transport used by both API clients; TLS 1.3 is negotiated when the server offers it. | `blob/provider/provider.go` |
| `hardened_site` | Met | The project sites send the hardening headers with non-permissive values: `strict-transport-security: max-age=31536000; includeSubdomains; preload`, `x-frame-options: deny`, `x-content-type-options: nosniff` and a restrictive `content-security-policy` (GitHub repository/releases and the Terraform Registry). | `https://github.com/caberdo/terraform-provider-rustfs` |
| `security_review` | **Unmet — see §5** | The assurance case and threat model exist and define the review's scope, but no dated security review against a released version has been performed yet; record it in `SECURITY.md` once done. | `blob/.github/SECURITY.md#security-review-status` |
| `hardening` | Met | See §4.3: hardening mechanisms are documented and in place. | `blob/.github/SECURITY.md#hardening-in-place` |
| `dynamic_analysis` | Met | Both dynamic analyses run before a release: the acceptance suite executes every resource against a real RustFS server (with coverage instrumentation), the unit suite runs under `-race`, and four fuzz targets (`FuzzPolicyDocument`, `FuzzAdminErrorBody`, `FuzzConfigKV`, `FuzzTargetConfig`) replay a committed seed corpus in every run with a weekly one-minute-per-target job in CI. | `blob/acc_test/docker-compose.yml`, `blob/.github/workflows/fuzz.yml` |

## 5. Open items — what is not met yet, and who can fix it

| # | Criterion (level) | What has to happen | Who |
|---|---|---|---|
| 1 | `test_statement_coverage90` (Gold) | **Met — this change.** Merged statement coverage is 90.2% (4609/5110) on the unit + acceptance profiles; see §5.1 for the statements that remain uncovered and why. The gate now enforces a 89.5% floor ([#79](https://github.com/caberdo/terraform-provider-rustfs/issues/79)). | Done |
| 2 | `access_continuity` (Silver) | Add a second person with `maintain`/`admin` on the organization and access to the release secrets; name them in `GOVERNANCE.md`. | Maintainer |
| 3 | `documentation_achievements` (Silver) | Merge the badge line from §3 (blocked on registration only). | Maintainer |
| 4 | `bus_factor` (Gold, MUST) | Bus factor ≥ 2: a second person who can create/close issues, merge changes and cut releases. Same act as item 2. | Maintainer |
| 5 | `contributors_unassociated` (Gold, MUST) | At least two significant contributors not associated with one another — a reviewer/contributor outside the `caberdo` organization. [#54](https://github.com/caberdo/terraform-provider-rustfs/issues/54) is the decision point, and the only honest route (a bot approving pull requests would make the badge claim something untrue). | Maintainer + community |
| 6 | `two_person_review` (Gold, MUST) | ≥50% of changes reviewed by someone other than the author: set `required_approving_review_count: 1` and add `CODEOWNERS` once item 5 exists. | Maintainer |
| 7 | `small_tasks` (Gold, MUST) | Label two or three beginner-sized issues `good first issue` (the label exists) so newcomers have a documented entry point. | Maintainer |
| 8 | `require_2FA` (Gold, MUST) | Confirm that the organization enforces 2FA for members with write access and record the confirmation. | Maintainer |
| 9 | `security_review` (Gold, MUST) | Perform and date a security review against the shipped requirements and boundary (the scope is written in `SECURITY.md`); a self-review by the maintainer against the threat model satisfies the criterion. | Maintainer |
| 10 | `vulnerabilities_fixed_60_days` (Passing, MUST) | Land [#49](https://github.com/caberdo/terraform-provider-rustfs/issues/49) (`x/net` + Go 1.27.2 + goldmark) so the answer is unambiguous instead of resting on the reachability argument in §6. | Code work |

**Realistic target:** items 2–3 and 10 unlock **Silver (Scorecard 7)**; items 4–9 are the price of
**Gold (10)** and are mostly about people, not code.

### 5.1 What the remaining statements are

`coverage-union.out` on the recorded revision covers 4609 of 5110 statements. The 501 that remain
are deliberate, not a backlog of untested behaviour:

| Scope | Uncovered | Why it is left uncovered |
|---|---|---|
| `.` (`main.go`) | 7 of 7 | The process entry point. `providerserver.Serve` runs until the host Terraform process closes the plugin connection and its error path ends in `log.Fatal`; every acceptance run executes it, but `go test -cover` cannot merge a profile from a subprocess. Covering it would need a test-only seam in `main`, which [#79](https://github.com/caberdo/terraform-provider-rustfs/issues/79) explicitly keeps out of scope. |
| `provider` | 467 of 3882 (88.0%) | The groups below. |
| `pkg/rustfs` | 27 of 1221 (97.8%) | Admin API failure replies the fixture cannot make the server produce (for example the error branches in `site_replication.go`). The package is already above 90%; a second fake server would assert the fake, not RustFS. |

The `provider` remainder falls into four groups:

* **Server states the fixture cannot force deterministically** — replication destinations and
  filters (`rustfs_bucket_replication_resource.go`, 55 statements), notification and audit target
  health transitions (26 + 21 + 16), group member races (21) and object-lock versioning
  combinations (19). A test would have to race a live server or pin an image-specific reply.
* **Optional-field flatteners** — branches for replication time/metrics, delete-marker
  replication, ownership and target attributes that the pinned example configurations do not set
  (`flattenReplicationTimeValue`, `flattenReplicationDestination`, `flattenReplicationFilter`,
  `flattenTopicTargets`, `flatWebhookAttributesSet`). They are exercised wherever the acceptance
  configuration sets the field; the remaining arms are configuration combinations the provider
  documents but the suite does not need to prove twice.
* **Defensive framework branches** — `newHTTPTransport`'s `SSL_CERT_FILE`/certificate-pool failures
  and nil/unknown model values that the schema prevents the framework from producing. The
  wrong-type `Configure` diagnostics are covered wherever a data source has a unit suite.
* **Error mappings for responses a passing run does not produce** — not-found fallbacks such as
  `isLifecycleNotFound` and per-service "gone" helpers. They are covered by the lifecycle-path
  tests where the resource owns the mapping; the remaining arms are the codes RustFS does not emit.

The import, trigger and no-op resources (`rustfs_iam_backup_import`,
`rustfs_bucket_metadata_backup_import`, `rustfs_rebalance`, `rustfs_bucket_object_lock`) have empty
`Delete` bodies: they contain no statements, so `go tool cover` prints `0.0%` for them without
lowering the total.

## 6. Judgement calls a reviewer may push back on

1. **`vulnerabilities_fixed_60_days`.** Two advisories are older than 60 days: goldmark (reachable
   only from the docs-generation tooling path `tools/` → `tfplugindocs`, never from the provider
   binary) and `x/crypto/openpgp` (a package the main module does not need, with no fixed version
   published). The table answers Met on that reachability argument; landing #49 removes the need for
   the argument entirely. If the reviewer disagrees, answer Unmet and re-answer after the bump.
2. **`crypto_used_network` (Gold).** The provider's transport requires TLS 1.2+ and verifies
   certificates, but the *scheme* comes from the `ssl` attribute, so a user who points the provider
   at a plaintext admin endpoint gets HTTP. The criteria demand that insecure protocols be "disabled
   by default"; the argued position is that the endpoint is mandatory and explicit, so the user
   always names the server they talk to. The alternative is to default `ssl = true`, which breaks
   every existing plaintext configuration — a decision for the maintainer, tracked here rather than
   decided silently.
3. **`test_branch_coverage80` (Gold).** Answered N/A: `go test -cover` reports statement coverage
   only and there is no established FLOSS branch-coverage tool for Go. A reviewer could ask for a
   third-party tool instead of N/A.
4. **`crypto_keylength`, `crypto_random`, `crypto_password_storage`.** N/A/Met here rests on the
   provider being a *client*: it generates no keys or nonces and stores no passwords. The RustFS
   server does those things, and it is a different project.
5. **`build_repeatable`.** The build is configured for reproducibility but CI does not perform a
   byte-for-byte rebuild comparison; if the reviewer asks for proof, the answer is to add that check
   to the release workflow.

## 7. Where the evidence lives

| Topic | File |
|---|---|
| What the software does, quick start, compatibility, verification of releases, coverage | [`README.md`](../README.md) |
| Security policy, requirements, threat model, assurance case, hardening, limitations | [`.github/SECURITY.md`](./SECURITY.md) |
| Contribution process, coding standards, review requirements, testing policy | [`.github/CONTRIBUTING.md`](./CONTRIBUTING.md) |
| Roles, decisions, continuity, how to add maintainers | [`GOVERNANCE.md`](../GOVERNANCE.md) |
| What the project intends to do (and not do) next year | [`ROADMAP.md`](../ROADMAP.md) |
| Runtime design and trust boundaries | [`ARCHITECTURE.md`](../ARCHITECTURE.md) |
| Upstream attribution and dependency licences | [`NOTICE`](../NOTICE) |
| Release plan of record | [`CHANGELOG.md`](../CHANGELOG.md) |
| CI: lint/gosec/unit/acceptance/coverage · fuzz targets + weekly job · docs drift · releases · Scorecard | [`.github/workflows/`](./workflows) |
| Licence | [`LICENSE`](../LICENSE) |

## 8. Re-measuring and keeping this pack true

* **Scorecard:** `curl -s https://api.securityscorecards.dev/projects/github.com/caberdo/terraform-provider-rustfs | jq -r '"overall \(.score)", (.checks[] | "\(.score)\t\(.name)")' | sort -n`
  (the workflow also runs weekly, Mondays 04:23 UTC, and on every push to `main`).
* **Badge level:** `curl -s 'https://www.bestpractices.dev/projects.json?url=https://github.com/caberdo/terraform-provider-rustfs'`
  — this is exactly what Scorecard reads.
* **Coverage:** `make coverage && make testacc-docker && make coverage-merge`, or read the
  `Coverage (unit + acceptance)` table in the acceptance job summary of the last `main` run.
* **Criteria drift:** BadgeApp edits `criteria.yml` over time; when a criterion in §4 is reworded,
  update the row rather than the answer. Re-check the MUST lists for a level before submitting.
* **When a level is awarded:** add the badge line (§3, replacing the previous one if the level
  changed), and mark the corresponding open items in §5 as done.
