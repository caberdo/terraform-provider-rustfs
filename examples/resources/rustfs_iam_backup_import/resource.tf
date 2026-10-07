# Supply the base64-encoded IAM backup ZIP, for example with filebase64().
variable "iam_backup_base64" {
  description = "Base64-encoded IAM backup ZIP contents"
  type        = string
  sensitive   = true
}

resource "rustfs_iam_backup_import" "restore" {
  content_base64 = var.iam_backup_base64
}
