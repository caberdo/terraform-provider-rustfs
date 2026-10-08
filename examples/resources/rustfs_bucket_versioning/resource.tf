# Enable bucket versioning.
resource "rustfs_bucket" "example" {
  name = "my-versioned-bucket"
}

resource "rustfs_bucket_versioning" "example" {
  bucket     = rustfs_bucket.example.name
  status     = "Enabled"
  mfa_delete = "Disabled"
}

# Suspend bucket versioning again.
resource "rustfs_bucket" "suspended" {
  name = "my-suspended-bucket"
}

resource "rustfs_bucket_versioning" "suspended" {
  bucket = rustfs_bucket.suspended.name
  status = "Suspended"
}

# Enable versioning together with MFA delete. mfa is a one-time value combining
# the MFA device serial number and the current code (format "<device> <code>").
resource "rustfs_bucket" "mfa" {
  name = "my-mfa-bucket"
}

resource "rustfs_bucket_versioning" "mfa" {
  bucket     = rustfs_bucket.mfa.name
  status     = "Enabled"
  mfa_delete = "Enabled"
  mfa        = "arn:aws:iam::123456789012:mfa/root-account-mfa-device 123456"
}