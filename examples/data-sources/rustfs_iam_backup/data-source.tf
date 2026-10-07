data "rustfs_iam_backup" "export" {}

output "iam_backup_base64" {
  value     = data.rustfs_iam_backup.export.content_base64
  sensitive = true
}
