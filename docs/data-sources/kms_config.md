---
page_title: "rustfs_kms_config Data Source - rustfs"
description: |-
  Get the RustFS KMS backend configuration
---

# rustfs_kms_config (Data Source)

Fetch the current RustFS KMS backend configuration, including the backend type, cache settings, and default key ID.

## Example Usage

```terraform
data "rustfs_kms_config" "current" {}

output "kms_backend" {
  value = data.rustfs_kms_config.current.backend
}
```

## Schema

### Read-Only

- `backend` (String) KMS backend type (local, vault-kv2, vault-transit, static, aws).
- `cache_enabled` (Boolean) Whether the KMS key cache is enabled.
- `cache_max_keys` (Number) Maximum number of keys held in the cache.
- `cache_ttl_seconds` (Number) Cache entry time-to-live in seconds.
- `default_key_id` (String) Default KMS key ID. Null when no default key is configured.
