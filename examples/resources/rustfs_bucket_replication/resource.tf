# Replicate objects under "logs/" with delete-marker handling, overwriting the
# destination storage class and replicating with SSE-KMS.
resource "rustfs_bucket" "source" {
  name = "source-bucket"
}

resource "rustfs_bucket_replication" "example" {
  bucket = rustfs_bucket.source.name
  role   = "arn:minio:replication::id:source-bucket"

  rule = [
    {
      id       = "replicate-logs"
      priority = 1
      status   = "Enabled"

      filter = {
        prefix = "logs/"
      }

      delete_marker_replication = {
        status = "Enabled"
      }

      existing_object_replication = {
        status = "Enabled"
      }

      destination = {
        bucket        = "arn:aws:s3:::dest-bucket"
        storage_class = "STANDARD"

        encryption_configuration = {
          replica_kms_key_id = "arn:aws:kms:us-east-1:123456789012:key/abcd"
        }
      }
    }
  ]
}

# A cross-account rule with tag-based filtering, source selection criteria and
# replication time control (RTC) with metrics.
resource "rustfs_bucket" "source_advanced" {
  name = "source-advanced-bucket"
}

resource "rustfs_bucket_replication" "advanced" {
  bucket             = rustfs_bucket.source_advanced.name
  role               = "arn:minio:replication::id:source-advanced-bucket"
  checksum_algorithm = "SHA256"

  rule = [
    {
      id       = "replicate-tagged"
      priority = 10
      status   = "Enabled"

      filter = {
        tag = {
          key   = "team"
          value = "core"
        }
      }

      delete_marker_replication = {
        status = "Disabled"
      }

      source_selection_criteria = {
        replica_modifications = {
          status = "Enabled"
        }
        sse_kms_encrypted_objects = {
          status = "Enabled"
        }
      }

      destination = {
        bucket        = "arn:aws:s3:::dest-advanced-bucket"
        account       = "111122223333"
        storage_class = "STANDARD_IA"
        access_control_translation = {
          owner = "Destination"
        }

        metrics = {
          status = "Enabled"

          event_threshold = {
            minutes = 15
          }
        }

        replication_time = {
          status = "Enabled"

          time = {
            minutes = 15
          }
        }
      }
    }
  ]
}