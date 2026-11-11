`rustfs_pools` now exposes a computed `pools` list with per-pool `status`, `availability`,
`capacity_status`, `pool_metadata_status`, `rebalance_metadata_status` and `observed_at` from the
RustFS 1.0.5 `pools/list` diagnostics. `names` is unchanged. Servers before 1.0.5 do not send the
diagnostics, so those attributes are null there instead of failing the read.
