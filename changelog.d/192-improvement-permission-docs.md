RustFS 1.0.5 tightened the admin permissions behind `rustfs_table_bucket`,
`rustfs_group`, `rustfs_serviceaccount`, `rustfs_user` and the
`rustfs_bucket_usage` data source. The generated docs now name the grant each
one needs (`admin:ClaimTableLocation`, `admin:RemoveUserFromGroup`, the
service-account parent scope, the access-key collision rule and the
bucket-scoped `s3:ListBucket` check) and the failure mode a scoped credential
sees.
