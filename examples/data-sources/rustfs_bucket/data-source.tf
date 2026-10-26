# Look up a bucket without managing it. A missing bucket reports exists = false
# without an error, so this can drive a count or for_each decision.
data "rustfs_bucket" "logs" {
  name = "my-logs-bucket"
}

output "logs_bucket_creation_date" {
  value = data.rustfs_bucket.logs.exists ? data.rustfs_bucket.logs.creation_date : null
}
