# An object lock configuration with a day-based compliance retention.
resource "rustfs_bucket" "example" {
  name                           = "my-locked-bucket"
  object_lock_enabled_for_bucket = true
}

resource "rustfs_bucket_object_lock" "example" {
  bucket              = rustfs_bucket.example.name
  object_lock_enabled = "Enabled"

  rule = {
    default_retention = {
      mode = "COMPLIANCE"
      days = 365
    }
  }
}

# A governance retention using years instead of days. days and years are
# mutually exclusive: exactly one of them must be set.
resource "rustfs_bucket" "governance" {
  name                           = "my-governance-bucket"
  object_lock_enabled_for_bucket = true
}

resource "rustfs_bucket_object_lock" "governance" {
  bucket              = rustfs_bucket.governance.name
  object_lock_enabled = "Enabled"

  rule = {
    default_retention = {
      mode  = "GOVERNANCE"
      years = 5
    }
  }
}