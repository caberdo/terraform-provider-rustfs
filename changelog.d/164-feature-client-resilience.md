The admin client now retries transient server answers instead of failing the
apply on the first `429`, `500`, `502`, `503`/`ServiceUnavailable`, `504`,
`SlowDown` or `RequestTimeout`, with three bounded, jittered backoffs and a
`Retry-After` header honoured. Retries are limited to reads and the routes
RustFS defines as idempotent (the scanner resets, a rebalance stop, a quota
set and a config set), so no job, credential or delete is replayed. A cancelled Terraform run stops the retry loop immediately.

Both API clients now send `terraform-provider-rustfs/<version>` in their
`User-Agent`, so server logs and bug reports can be tied to a provider build.
