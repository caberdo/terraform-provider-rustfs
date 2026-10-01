---
page_title: "rustfs_kms_status Data Source - rustfs"
description: |-
  Get the RustFS KMS service status
---

# rustfs_kms_status (Data Source)

Fetch the current RustFS KMS service status, including backend health, cache stats, capabilities, and cluster config.

## Example Usage

```terraform
data "rustfs_kms_status" "current" {}

output "kms_backend" {
  value = data.rustfs_kms_status.current.backend_type
}
```

## Schema

### Read-Only

- `backend_status` (String) KMS backend health status (healthy, unhealthy, error).
- `backend_type` (String) KMS backend type (local, vault-kv2, vault-transit, static, aws).
- `cache_enabled` (Boolean) Whether the KMS key cache is enabled.
- `cache_stats` (Object) KMS cache statistics. Null when caching is disabled.
  - `hit_count` (Number) Number of cache hits.
  - `miss_count` (Number) Number of cache misses.
  - `entry_count` (Number) Number of cache entries.
  - `eviction_count` (Number) Number of cache evictions.
- `capabilities` (Map of Boolean) Map of KMS backend capabilities. Present only on servers that report it.
- `cluster_config` (Object) Cluster-wide KMS configuration state. Present only on servers that report it.
  - `consistent` (Boolean) Whether all nodes report the same KMS config fingerprint.
  - `nodes` (List of Object) Per-node KMS configuration state.
    - `host` (String) Node host or address.
    - `config_fingerprint` (String) Node KMS config fingerprint. Null when the node has no KMS config.
    - `error` (String) Node KMS error, if any.
- `default_key_id` (String) Default KMS key ID. Null when no default key is configured.