// The server reads the backend type from `type` inside config_json; tier_type
// carries the same value for readability. Backend credentials are camelCase
// (accessKey/secretKey) and the backend block is nested under the type name.
resource "rustfs_tier" "s3_cold" {
  name      = "S3COLD"
  tier_type = "s3"
  config_json = jsonencode({
    type = "s3"
    s3 = {
      name       = "S3COLD"
      endpoint   = "https://s3.amazonaws.com"
      accessKey  = "AKIAIOSFODNN7EXAMPLE"
      secretKey  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
      region     = "us-east-1"
      bucket     = "cold-storage-backup"
    }
  })

  // force_destroy only changes what destroy does: it removes the tier record
  // even when RustFS reports the backend as non-empty or still in use, and
  // RustFS 1.0.5 can then skip the backend probe. Remote objects are never
  // deleted. Leave it false and let destroy explain the blockers first.
  force_destroy = false
}
