# List every KMS master key the server reports. The route returns metadata
# only; no key material is ever exposed.
data "rustfs_kms_keys" "all" {}

output "kms_key_ids" {
  value = [for key in data.rustfs_kms_keys.all.keys : key.key_id]
}

output "kms_disabled_keys" {
  value = [
    for key in data.rustfs_kms_keys.all.keys : key.key_id
    if key.status == "Disabled"
  ]
}
