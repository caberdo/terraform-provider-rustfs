`rustfs_bucket` now also exists as a read-only data source, and `rustfs_buckets` lists every bucket. The
single-bucket data source reports `exists` (a missing bucket is `false` without an error, so it can
drive `count`/`for_each`), `creation_date` from `ListBuckets` and `region` from `GetBucketLocation`.
`rustfs_buckets` returns one `ListBuckets` result sorted by name instead of issuing a request per
bucket, and both fail with a diagnostic naming `s3:ListAllMyBuckets`/`s3:ListBucket` when the
credentials lack the permission rather than returning an empty list.
