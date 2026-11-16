# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | ✅ |
| < latest| ❌ |

Only the latest released version receives security updates.

## Reporting a Vulnerability

If you discover a security vulnerability, please **DO NOT** open a public issue.

**Preferred Method:** Use GitHub's [Private Vulnerability Reporting](https://github.com/caberdo/terraform-provider-rustfs/security/advisories/new)

### What to Include

* **Vulnerability Type**: What type of vulnerability is it
* **Affected Versions**: Which provider versions are affected
* **Impact**: What is the impact (data exposure, privilege escalation, etc.)
* **Reproduction Steps**: Detailed steps to reproduce
* **Proof of Concept**: If possible, include a minimal PoC

### What happens then

| Step | Target |
|---|---|
| We acknowledge the report privately | within 48 hours |
| We assess it, reproduce it and agree the severity with you | within 7 days |
| We release a fix in a patch release and publish a GitHub security advisory | as soon as a fix is verified, typically within 14 days |
| We credit you in the advisory and the release notes | at disclosure, unless you ask to stay anonymous |

If the report turns out not to be a vulnerability, we say so and explain why. Reports about
*RustFS itself* (the server) belong to the [RustFS project](https://github.com/rustfs/rustfs), not
here; we will point you at it if a report lands with us by mistake.

Reporter credit: every resolved report is credited by name (or handle) in the advisory and the
release notes unless the reporter requests anonymity.

## Security requirements — what you can and cannot expect

What this provider guarantees:

* **TLS is verified by default.** With `ssl = true` the transport requires TLS 1.2 or newer and
  verifies the server certificate against the system roots (plus `SSL_CERT_FILE` if set).
  Verification is only skipped when `insecure = true` is configured explicitly, which the schema
  and README describe as being for self-signed test servers.
* **Credentials are never logged and never written by the provider.** `secret_key` and
  `access_secret` are `Sensitive`, so Terraform redacts them from plan/apply output; the provider
  itself writes no files. Credentials do live in *your* state file if you put them in the
  configuration — protect the state (see the best practices below).
* **Requests are signed.** Every admin API call is SigV4-signed over the method, path, query,
  headers and the SHA-256 of the body (`X-Amz-Content-Sha256`), so a tampered request is rejected
  by the server. S3 calls are signed by the AWS SDK for Go v2.
* **No code execution, no shell, no SQL.** The provider makes HTTP requests and decodes responses
  into typed Go structs. It never invokes `os/exec`, never opens a local socket as a server, and
  ships no vendored native code.
* **Releases are signed and reproducible.** GPG-signed, GitHub-verified tags; `SHA256SUMS` signed
  with the release key published at [`.github/release-signing-key.asc`](./release-signing-key.asc);
  an SPDX SBOM per release. Terraform records the provider checksums in `.terraform.lock.hcl`.

What it does **not** guarantee:

* It does not make your RustFS configuration secure. A bucket policy or a user policy that grants
  `*` is applied exactly as written — the provider is a faithful client, not a policy advisor.
* It does not protect a compromised RustFS server or a compromised credentials set: those
  credentials usually have admin rights on that server by design.
* It does not encrypt your state file and does not manage your Terraform backend.
* It does not scan the objects or policies you manage; it validates its own inputs (enums, types,
  required combinations), not their security consequences.

## Threat model and trust boundaries

```
[ your Terraform configuration / environment ]                    (trusted input)
[ Terraform CLI  ──plugin protocol (stdio)──►  provider process ] (same host, your privileges)
                        │                                  │
                        │ signed HTTPS/S3 ◄──────────────┐ │
                        ▼                                │ │
              [ RustFS server :9000/:9001 ]              │ │  (semi-trusted: has your data,
                        │                                │ │   answers untrusted bytes)
                        │                                │ │
[ Terraform state (sensitive values) ] ◄─────────────────┘ │  (your responsibility)
[ GitHub Releases / Registry ──signed tag + signed sums ────┘  (release supply chain)
```

| Boundary | Trusted side | Untrusted side | Controls |
|---|---|---|---|
| B1 Terraform ↔ provider plugin | both (same host, user's privileges) | — | typed request/response structs; no dynamic evaluation |
| B2 Provider ↔ RustFS server | provider | server responses are parsed as bytes | TLS 1.2+ verified by default; SigV4 request signing; typed `encoding/xml` + `encoding/json` decoding; error bodies stringified, never executed |
| B3 Provider ↔ Terraform state | — | state may be shared/stored remotely | `Sensitive` on credential attributes; README documents state protection; provider writes no files |
| B4 Release artifacts ↔ user | GitHub + GPG signature | the network | signed tags, GPG-signed `SHA256SUMS`, SBOM, lock-file checksums |
| B5 Maintainer ↔ repository | maintainer | — | PR-only `main` with required `ci / Lint & Security`, `ci / Test`, `ci / Coverage` and `examples / Validate Examples` checks, no force-push, no deletion, review threads resolved (see [`GOVERNANCE.md`](../GOVERNANCE.md)) |

The asset that matters most is the **admin credential** for the RustFS server: it is used for
signed admin calls and travels in the Terraform configuration/environment and the state file, not
through the provider's logs.

## Assurance case

Why the security requirements above hold, and which common weaknesses were considered:

| Principle / weakness class | How it is addressed |
|---|---|
| **Economy of mechanism** | Two HTTP clients and one `AllClient` handed to every resource; no caching layer, no background goroutines, no plugin-global state (`provider/all_client.go`, `provider/provider.go`) |
| **Fail-safe defaults** | Certificate verification on (`insecure` is opt-in and defaults to false); missing endpoint is a hard error instead of a guessed default; enum attributes are allow-listed (`stringvalidator.OneOf`, 31 uses) with no permissive fallback |
| **Complete mediation** | Every admin request goes through `doRequest`/`DoDirectRequest`, which sign the request in one place (`pkg/rustfs/admin_client.go`); resources never build raw HTTP calls |
| **Open design** | This document, [`ARCHITECTURE.md`](../ARCHITECTURE.md) and the public source describe the whole mechanism; no security depends on secrecy of the design |
| **Least privilege / separation** | The provider holds only the credentials you give it, requests only the routes it needs, and exposes no privileged helper beyond those routes |
| Injection (CWE-78/89) | No `os/exec`, no SQL, no template evaluation; all values travel as typed JSON/XML payloads |
| XML external entities (CWE-611) | Go's `encoding/xml` does not expand external entities; error bodies are decoded into a two-field struct |
| Insecure deserialization (CWE-502) | `encoding/json`/`encoding/xml` into concrete structs; no `unsafe`, no reflection-driven construction from input |
| Sensitive data exposure (CWE-532) | Credentials are `Sensitive`; logging is limited to fixed `tflog` messages with no attribute values |
| Man-in-the-middle (CWE-295) | TLS 1.2 minimum, system root verification, `X-Amz-Content-Sha256` body integrity, signed releases for the binary itself |
| ReDoS | The only regular expressions are fixed patterns in dependencies; no user input is compiled into a regex |
| Dependency risk (CWE-1395) | Dependabot (gomod, docker-compose, actions) plus gosec and OpenSSF Scorecard in CI; SPDX SBOM per release; every direct dependency is a Go module with a public licence recorded in [`NOTICE`](../NOTICE) |

### Hardening in place

* `-trimpath` and `CGO_ENABLED=0` in the release build (`.github/goreleaser.yml`).
* CI runs `go vet`, golangci-lint (staticcheck, errcheck, bodyclose, govet with everything but
  field alignment) and gosec with SARIF upload to GitHub code scanning.
* Response bodies are drained and closed on every path (`drainClose`), so connection reuse does not
  mask errors.
* Errors preserve the server body verbatim (`APIError.Raw`), so a misleading provider message
  cannot hide what the server said.

### Known limitations

These are accepted, documented risks rather than unknowns:

1. **Names interpolated into admin paths are not percent-escaped** (`RelPath: "quota/" + bucket`
   and friends in `pkg/rustfs`). A name containing `/`, `?` or `#` can address a different route on
   the *same* server, with the credentials you supplied to that server. RustFS validates names of
   the objects it creates, and the values come from your own configuration, so the exposure is a
   misdirected request rather than a privilege boundary crossing. Escaping them is the sensible
   hardening step.
2. **Response bodies are read without a size limit** (`io.ReadAll` in `pkg/rustfs`). A hostile or
   broken server can return an arbitrarily large body and exhaust provider memory. A limit cannot
   simply be small, because IAM and bucket-metadata backups are legitimately large ZIP archives.
3. **`insecure = true` disables verification for the whole provider process** (both clients), which
   is what it says on the tin; it is intended for test servers.
4. **`http.Client` follows up to 10 redirects** as the Go standard library does, and the standard
   library strips the `Authorization` header when a redirect changes host (so the SigV4 signature
   is not replayed to a third party). RustFS does not redirect admin routes in practice.

### Security review status

The threat model, trust boundaries and assurance case above are the scope of the project's security
review. It has not yet been performed as a dated, recorded exercise against a released version;
that review (and the resolution of the limitations above) is tracked in
[#53](https://github.com/caberdo/terraform-provider-rustfs/issues/53).

## Dependency Vulnerability Scanning

Every pull request runs `govulncheck ./...` (the `Vulnerability Scan` job in
`.github/workflows/test.yml`), so a newly published advisory that is reachable from the provider
fails CI instead of only lowering the OpenSSF Scorecard result.

One module-level advisory is accepted and tracked here:

| Advisory | Package | Status |
|---|---|---|
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | `golang.org/x/crypto/openpgp` | Accepted: no fixed version exists |

`golang.org/x/crypto/openpgp` is unmaintained and unsafe by design, so there is no release that
fixes the advisory. The provider does not import it, and the released binary does not contain
`golang.org/x/crypto`: the module is in `go.mod` only because `golang.org/x/crypto/ssh` is
required by `terraform-plugin-testing`, which is used exclusively by the acceptance tests.
Removing the module means dropping the acceptance-test framework, so the advisory is documented
instead. Re-check this entry when `terraform-plugin-testing` changes its dependency graph.

## Security Best Practices for Users

1. **Credential Management**
   - Use environment variables (`RUSTFS_USER`, `RUSTFS_SECRET`) instead of hardcoded credentials
   - Rotate credentials regularly

2. **Network Security**
   - Use TLS (`ssl = true`) for production deployments
   - Consider private networks for sensitive data

3. **State Protection**
   - Encrypt Terraform state files
   - Use remote state backends with proper access controls

4. **Monitoring**
   - Enable RustFS audit logging
   - Review provider logs for accidental credential exposure
