`rustfs_bucket` checks `create_bucket_configuration.location_constraint` against the region the
server reports before it calls `CreateBucket`, so a mismatch now fails with a diagnostic naming the
server's region and the accepted values instead of RustFS 1.0.5's `InvalidLocationConstraint`. A
constraint that matches the region, the `EU` alias for `eu-west-1`, and a deployment that reports
no region all keep working; the default-region path (no `create_bucket_configuration`) is
unchanged. The `location_constraint` description in `docs/resources/bucket.md` states the rule.
