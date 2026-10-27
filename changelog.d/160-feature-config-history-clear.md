`rustfs_config_history_clear` deletes every stored server-config snapshot
through `DELETE /rustfs/admin/v3/clear-config-history-kv`. This removes all
rollback points, so `rustfs_config_restore` can no longer reach a snapshot and
an earlier configuration mistake can no longer be undone through the provider.
The route returns an empty 200 with no count, so the resource records only
`cleared_at`; `dry_run` and `entries_cleared` do not exist. The resource is an
action: Read echoes the recorded result, Delete is a no-op, and `confirm` must
be the literal `clear-config-history`.
