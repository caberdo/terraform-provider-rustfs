# Governance

How decisions are made in `caberdo/terraform-provider-rustfs`, who is responsible for what, and
what happens if a maintainer steps away. Kept deliberately short: this is a single-repository,
single-maintainer project today, and the document says so instead of describing a committee that
does not exist.

## Roles

| Role | Who | Responsibilities |
|---|---|---|
| **Maintainer** | [@dennismdejong](https://github.com/dennismdejong) | Final say on scope and design; review and merge pull requests; cut releases (signed tags, GoReleaser run); triage issues; handle private vulnerability reports; hold the release signing key and the `caberdo` organization's repository access |
| **Organization owner** | the `caberdo` GitHub organization | Owns the repository, the Terraform Registry namespace (`caberdo/rustfs`) and the DNS-independent release identity |
| **Contributor** | anyone | Propose changes through pull requests; file reproducible bug reports; participate in issue discussion. Contribution requirements are in [`CONTRIBUTING.md`](./.github/CONTRIBUTING.md) |
| **Security reporter** | anyone | Report vulnerabilities privately, as described in [`SECURITY.md`](./.github/SECURITY.md); reporters are credited in the release notes unless they ask otherwise |

There is no separate release manager, documentation owner or triage team: the maintainer holds
those responsibilities and the automations in `.github/workflows/` do the mechanical parts.

## How decisions are made

1. **Anything non-trivial starts as an issue.** Bugs and features arrive through the issue forms in
   `.github/ISSUE_TEMPLATE/`. Design questions are decided *in* the issue, with the alternatives and
   the reasoning written down (see [#54](https://github.com/caberdo/terraform-provider-rustfs/issues/54)
   for an example where the options and their trade-offs are recorded before a choice is made).
2. **Every change lands through a pull request against `main`.** Direct pushes are impossible: the
   `protect-main` ruleset requires a pull request, requires the `ci / Lint & Security`,
   `ci / Test`, `ci / Coverage` and `examples / Validate Examples` checks, forbids force-pushes and
   deletions, dismisses stale reviews and requires review conversations to be resolved.
3. **The maintainer decides.** Where an issue reaches no consensus, the maintainer records the
   decision and the rationale in the issue and moves on. Disagreement is expressed in the issue, not
   by re-litigating a merged change.
4. **Releases are cut, not continuous.** `Release` publishes a tag: a maintainer creates and pushes
   a GPG-signed tag, or dispatches `Release` with an existing tag to (re)publish it. The version
   scheme and the release process are described in [`README.md`](./README.md#verifying-a-release)
   and [`CHANGELOG.md`](./CHANGELOG.md); breaking changes are listed under `BREAKING CHANGES` and
   Terraform-visible schema changes carry `RequiresReplace()`.
5. **Backwards compatibility is a commitment.** Terraform type and attribute names are public API:
   renaming `rustfs_bucket` or an attribute silently breaks user state, so it is not done (the
   migration to a `internal/` Go package layout described in [`AGENTS.md`](./AGENTS.md) moves Go
   files only).

## Continuous integration as the gate

| Check | Workflow | Runs on |
|---|---|---|
| `ci / Lint & Security` (golangci-lint, gosec → SARIF) | `test.yml` | every pull request and push to `main` |
| `ci / Test` (build, vet, `go test -race` with coverage) | `test.yml` | every pull request and push to `main` |
| `ci / Coverage` (merged unit + acceptance profile against the floor) | `test.yml` | every pull request |
| `examples / Validate Examples` (fragments unioned against the provider schema) | `test.yml` | every pull request and push to `main` |
| `acceptance / Acceptance` (RustFS matrix in Docker, stable roll-up) | `test.yml` | push to `main` |
| `ci / Generate Docs` (docs drift) / `ci / Validate Markdown Files` | `test.yml` | push to `main` (and tags) / pull request and push to `main` |
| `analysis / Scorecard analysis` (OpenSSF, published to the Scorecard API) | `scorecard.yml` | weekly, push to `main`, branch-protection changes |

## Access and continuity

The project must be able to keep working if one person becomes unavailable. What exists today:

* The repository, registry namespace and release identity are owned by the `caberdo` organization,
  not by a personal account, so ownership does not disappear with an individual.
* The release key pair and its passphrase are stored as encrypted GitHub Actions secrets
  (`GPG_PRIVATE_KEY`, `PASSPHRASE`), used only by the release workflows, and the public half is
  committed at [`.github/release-signing-key.asc`](./.github/release-signing-key.asc);
  the process needed to publish a release is documented in
  [`README.md`](./README.md#verifying-a-release) and `.github/workflows/`.
* Every release is reproducible from a tag by anyone with access to the repository and the secrets.

**Open gap — bus factor.** There is currently one maintainer. To satisfy the continuity
requirement ("the project can still create and close issues, accept changes and release within a
week if any one person is lost") a *second* person needs:

1. `maintain` (or `admin`) access to the `caberdo` organization,
2. membership of the secret-holding team (GPG private key, passphrase, `GITHUB_TOKEN` is
   automatic), and
3. a documented hand-over in this file naming them.

Until that is done, continuity depends on the organization remaining reachable by its current
owner — which is exactly the risk this section is here to make visible.

## Adding maintainers and reviewers

1. The candidate contributes at least one reviewed change and shows they can run the acceptance
   suite locally (`make testacc-live`, Podman).
2. An existing maintainer invites them as a collaborator, then grants organization `maintain`.
3. If they are expected to review, `protect-main` gains `required_approving_review_count: 1` and
   `.github/CODEOWNERS` starts covering `provider/` and `.github/workflows/`; the decision and its
   date are recorded in the issue that proposed it (tracked as
   [#54](https://github.com/caberdo/terraform-provider-rustfs/issues/54)).
4. Their name is added to the table above.

## Changing this document

Governance changes are proposed by pull request like any other change. If a second maintainer
joins, the "Roles" and "Access and continuity" tables are updated in the same pull request that
grants the access.
