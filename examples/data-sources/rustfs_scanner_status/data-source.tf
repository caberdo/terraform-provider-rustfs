# Observe the scanner; tuning stays in the scanner subsystem of rustfs_config
# or in the server environment (RUSTFS_SCANNER_* variables).
data "rustfs_scanner_status" "current" {}

output "scanner_freshness" {
  value = data.rustfs_scanner_status.current.freshness_state
}

output "scanner_last_cycle" {
  value = {
    result      = data.rustfs_scanner_status.current.last_cycle_result
    duration    = data.rustfs_scanner_status.current.last_cycle_duration_seconds
    objects     = data.rustfs_scanner_status.current.last_cycle_objects_scanned
    directories = data.rustfs_scanner_status.current.last_cycle_directories_scanned
  }
}

output "scanner_usage_rebuild_pending" {
  value = data.rustfs_scanner_status.current.usage_bootstrap_rebuild_pending
}

# RustFS 1.0.5 reports the most recent usage-publication attempt separately from
# usage_publication_state. On 1.0.1 through 1.0.4 these attributes are null.
output "scanner_usage_publication" {
  value = {
    class            = data.rustfs_scanner_status.current.usage_publication_class
    outcome          = data.rustfs_scanner_status.current.usage_publication_outcome
    pending_buckets  = data.rustfs_scanner_status.current.usage_publication_pending_bucket_count
    snapshot_updated = data.rustfs_scanner_status.current.usage_publication_snapshot_updated_at_unix_secs
  }
}

# Heal observation is read-only: it never starts, pauses or cancels a heal.
# The candidate arrays stay available as compact JSON for entries whose values
# the provider does not name.
output "scanner_heal_observation" {
  value = {
    retained_pending = data.rustfs_scanner_status.current.heal_retained_pending_tasks
    terminal         = data.rustfs_scanner_status.current.heal_terminal_candidate_count
    unknown          = data.rustfs_scanner_status.current.heal_unknown_candidate_count
    integrity_state  = data.rustfs_scanner_status.current.heal_integrity_state
    candidates       = data.rustfs_scanner_status.current.heal_candidates_json
  }
}
