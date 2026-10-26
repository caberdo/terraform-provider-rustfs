# Switch S3 transfer acceleration on or off for a bucket. Destroying this
# resource sets Suspended, because S3 has no route that removes the
# configuration; the bucket itself is never deleted.
resource "rustfs_bucket" "accelerated" {
  name = "my-accelerated-bucket"
}

resource "rustfs_bucket_accelerate" "accelerated" {
  bucket = rustfs_bucket.accelerated.name
  status = "Enabled"
}
