`rustfs_site_replication_status` reads the whole site-replication group
(`GET /rustfs/admin/v3/site-replication/info`,
`GET /rustfs/admin/v3/site-replication/status` and
`GET /rustfs/admin/v3/site-replication/metainfo`): which peers exist, each
peer's `deployment_id`, sync state and TLS/ILM settings, per-site replicated and
total counters for every replicated family, unreachable peers with their error,
the resync retry queue, and the topology counts from the metainfo route. The
metainfo payload itself stays out of state; only counts and identity are
reported. A server that answers the status route as unimplemented still yields
the info and metainfo result with `status_available = false`; any other status
error fails the read.

`rustfs_site_replication_resync` drives
`PUT /rustfs/admin/v3/site-replication/resync/op` with `operation = "start"`,
`"cancel"` or `"status"`. A resync re-copies the replicated buckets between
sites, so it can move a large amount of data; every operation names the peer by
`deployment_id`, which `rustfs_site_replication_status` reports. Every
configurable attribute carries RequiresReplace, so a change runs a new operation
instead of editing the recorded one. Destroying the resource does nothing on the
server: cancelling is an explicit `operation = "cancel"` apply, never a side
effect of `terraform destroy`.
