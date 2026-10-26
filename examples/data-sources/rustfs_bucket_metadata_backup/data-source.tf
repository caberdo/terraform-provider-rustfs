data "rustfs_bucket_metadata_backup" "export" {}

# The data source returns the server's bucket-metadata export as a base64 ZIP.
# Decode it before handing the archive to your own tooling or storage.
output "bucket_metadata_zip_size" {
  description = "Size of the decoded bucket-metadata ZIP archive in bytes."
  value       = length(base64decode(data.rustfs_bucket_metadata_backup.export.content_base64))
}
