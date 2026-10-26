# Ask the server whether a bucket is public. RustFS 1.0.x reports true only for a
# statement that grants `s3:*` on the bucket to an explicit `{"AWS": ["*"]}`
# principal; public s3:GetObject policies and canned ACLs report false.
data "rustfs_bucket_policy_status" "app" {
  bucket = "my-app-bucket"
}

output "app_bucket_is_public" {
  value = data.rustfs_bucket_policy_status.app.is_public
}
