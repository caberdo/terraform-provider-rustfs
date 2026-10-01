data "rustfs_kms_config" "current" {}

output "kms_backend" {
  value = data.rustfs_kms_config.current.backend
}