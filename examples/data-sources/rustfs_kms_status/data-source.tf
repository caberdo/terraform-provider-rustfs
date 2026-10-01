data "rustfs_kms_status" "current" {}

output "kms_backend" {
  value = data.rustfs_kms_status.current.backend_type
}
