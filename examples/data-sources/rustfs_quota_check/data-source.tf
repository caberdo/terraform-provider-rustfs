# Ask the quota subsystem whether an operation would be admitted. The check is
# read-only: it changes no quota, object or usage. Unknown operation types are
# treated by RustFS as PUT; this provider accepts PUT, POST, COPY and DELETE.
data "rustfs_quota_check" "next_upload" {
  bucket         = "logs"
  operation_type = "PUT"
  operation_size = 1048576
}

output "upload_fits_quota" {
  value = {
    allowed   = data.rustfs_quota_check.next_upload.allowed
    remaining = data.rustfs_quota_check.next_upload.remaining_quota
    usage     = data.rustfs_quota_check.next_upload.current_usage
  }
}
