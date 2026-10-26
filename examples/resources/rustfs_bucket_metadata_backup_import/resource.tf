# Supply the base64-encoded bucket-metadata ZIP, for example with filebase64().
variable "bucket_metadata_backup_base64" {
  description = "Base64-encoded bucket-metadata ZIP contents"
  type        = string
  sensitive   = true
}

resource "rustfs_bucket_metadata_backup_import" "restore" {
  content_base64 = var.bucket_metadata_backup_base64
}
