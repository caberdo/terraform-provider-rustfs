# Complete RustFS Example

This example is an end-to-end RustFS configuration that shows the provider's
resources working together:

- **Bucket** - versioning, encryption, object lock and public-access blocking.
- **Lifecycle** - expiration and transition rules per prefix.
- **Tags / CORS / quota** - metadata, browser access and a 10 GiB limit.
- **IAM** - a policy, a group with members, and a user, wired together with the
  policy-attachment resources.
- **Service account** - a scoped credential for a CI pipeline.
- **Data sources** - read the users, policies, pools and quota back.

Provider configuration is intentionally not included; add a provider block here
or copy `examples/provider/provider.tf` next to this file. The configuration
validates on its own, but applying it requires a reachable RustFS endpoint.
