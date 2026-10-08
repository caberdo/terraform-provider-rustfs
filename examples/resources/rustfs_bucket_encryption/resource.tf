# Server-side encryption with the default AES256 algorithm.
resource "rustfs_bucket" "encrypted" {
  name = "my-encrypted-bucket"
}

resource "rustfs_bucket_encryption" "example" {
  bucket = rustfs_bucket.encrypted.name

  rule = [
    {
      apply_server_side_encryption_by_default = {
        sse_algorithm = "AES256"
      }
      bucket_key_enabled = false
    },
  ]

  checksum_algorithm = "SHA256"
}

# SSE-KMS with an explicit customer managed key and a bucket key.
resource "rustfs_bucket" "encrypted_kms" {
  name = "my-encrypted-kms-bucket"
}

resource "rustfs_bucket_encryption" "kms" {
  bucket = rustfs_bucket.encrypted_kms.name

  rule = [
    {
      apply_server_side_encryption_by_default = {
        sse_algorithm     = "aws:kms"
        kms_master_key_id = "arn:aws:kms:us-east-1:123456789012:key/abcd"
      }
      bucket_key_enabled = true
    },
  ]
}