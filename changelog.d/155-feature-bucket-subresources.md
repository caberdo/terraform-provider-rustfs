Four bucket sub-resources are now modelled: `rustfs_bucket_website` (index/error documents, redirect-all
and routing rules), `rustfs_bucket_logging` (target bucket and prefix, and the target grants), and
`rustfs_bucket_accelerate` and `rustfs_bucket_request_payment`. All four round-trip on RustFS 1.0.1
through 1.0.3, so a plan after apply is empty; `rustfs_bucket_accelerate` models only `status`,
because that is the only field the S3 accelerate document carries. Destroying them removes the configuration (logging is
disabled; acceleration is set to `Suspended`; request payment returns to `BucketOwner`) and never
deletes the bucket. `rustfs_bucket_logging` refuses a target bucket that does not exist instead of
silently logging into nowhere.
