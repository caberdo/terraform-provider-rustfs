# Send access logs from one bucket to another. The target bucket must already
# exist: this resource never creates it and fails with a diagnostic naming the
# missing bucket, so depends_on is the way to order the two.
resource "rustfs_bucket" "app" {
  name = "my-app-bucket"
}

resource "rustfs_bucket" "logs" {
  name = "my-log-bucket"
}

resource "rustfs_bucket_logging" "app" {
  bucket        = rustfs_bucket.app.name
  target_bucket = rustfs_bucket.logs.name
  target_prefix = "access-logs/"

  target_grant {
    permission = "FULL_CONTROL"

    grantee {
      type = "Group"
      uri  = "http://acs.amazonaws.com/groups/s3/LogDelivery"
    }
  }

  depends_on = [rustfs_bucket.logs]
}
