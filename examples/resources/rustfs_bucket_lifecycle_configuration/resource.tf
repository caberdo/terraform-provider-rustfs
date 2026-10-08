# Multiple lifecycle rules on one bucket: expiry with a prefix filter, a
# transition to a warm/cold ILM tier for noncurrent versions, and size-based
# filtering combined with tags.
resource "rustfs_bucket" "example" {
  name = "my-lifecycle-bucket"
}

resource "rustfs_bucket_lifecycle_configuration" "example" {
  bucket = rustfs_bucket.example.name

  rule = [
    {
      id     = "expire-logs"
      status = "Enabled"

      filter = {
        prefix = "logs/"
      }

      expiration = {
        days = 30
      }

      abort_incomplete_multipart_upload = {
        days_after_initiation = 7
      }
    },
    {
      id     = "archive-old"
      status = "Enabled"

      filter = {
        prefix = "archive/"
      }

      transition = [
        {
          days          = 60
          storage_class = "WARM"
        }
      ]

      noncurrent_version_transition = [
        {
          noncurrent_days           = 30
          newer_noncurrent_versions = 3
          storage_class             = "COLD"
        }
      ]

      noncurrent_version_expiration = {
        noncurrent_days = 365
      }
    },
    {
      id     = "expire-tagged-large-objects"
      status = "Enabled"

      filter = {
        and = {
          prefix = "reports/"

          tags = [
            {
              key   = "tier"
              value = "cold"
            }
          ]

          object_size_greater_than = 1048576
        }
      }

      expiration = {
        date = "2027-01-01T00:00:00Z"
      }
    }
  ]
}

# A rule with a single tag filter, noncurrent-version expiry limited to the
# newest offenders, and the minimum object size that triggers transitions.
resource "rustfs_bucket" "tagged" {
  name = "my-tagged-lifecycle-bucket"
}

resource "rustfs_bucket_lifecycle_configuration" "tagged" {
  bucket                                 = rustfs_bucket.tagged.name
  transition_default_minimum_object_size = "all_storage_classes_128K"

  rule = [
    {
      id     = "expire-tagged"
      status = "Enabled"

      filter = {
        tag = {
          key   = "retention"
          value = "short"
        }
      }

      expiration = {
        days = 14
      }

      noncurrent_version_expiration = {
        noncurrent_days           = 3
        newer_noncurrent_versions = 2
      }
    }
  ]
}