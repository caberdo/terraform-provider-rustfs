`rustfs_quota_stats` reads what the quota subsystem sees for a bucket
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
