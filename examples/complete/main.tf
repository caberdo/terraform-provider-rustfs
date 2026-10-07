# Complete RustFS Example
#
# End-to-end configuration showing the resources working together: a versioned,
# encrypted, quota-limited bucket, an IAM group and user scoped by a policy, a
# service account for CI, hardening rules, and data sources that read the result
# back.
#
# Provider configuration is intentionally left out; add it here or copy it from
# examples/provider/provider.tf. Everything below validates without a running
# server, but `terraform apply` needs a reachable RustFS endpoint.

# ---------------------------------------------------------------------------
# BUCKET - storage container with its feature set
# ---------------------------------------------------------------------------
resource "rustfs_bucket" "data" {
  name = "app-data"
}

resource "rustfs_bucket_versioning" "data" {
  bucket = rustfs_bucket.data.name
  status = "Enabled"
}

resource "rustfs_bucket_encryption" "data" {
  bucket    = rustfs_bucket.data.name
  algorithm = "AES256"
}

resource "rustfs_bucket_object_lock" "data" {
  bucket = rustfs_bucket.data.name
  mode   = "COMPLIANCE"
  days   = 365
}

resource "rustfs_bucket_public_access_block" "data" {
  bucket                  = rustfs_bucket.data.name
  block_public_acls       = true
  ignore_public_acls      = true
  block_public_policy     = true
  restrict_public_buckets = true
}

resource "rustfs_bucket_tags" "data" {
  bucket = rustfs_bucket.data.name

  tags = {
    environment = "production"
    team        = "platform"
  }
}

resource "rustfs_bucket_lifecycle_configuration" "data" {
  bucket = rustfs_bucket.data.name

  rule {
    id     = "expire-logs"
    status = "Enabled"

    filter {
      prefix = "logs/"
    }

    expiration {
      days = 30
    }
  }

  rule {
    id     = "archive-documents"
    status = "Enabled"

    filter {
      prefix = "documents/"
    }

    transition {
      days          = 60
      storage_class = "WARM"
    }

    noncurrent_version_expiration {
      noncurrent_days = 365
    }
  }
}

resource "rustfs_bucket_cors" "data" {
  bucket = rustfs_bucket.data.name

  rule {
    id              = "webapp"
    allowed_origins = ["https://app.example.com"]
    allowed_methods = ["GET", "PUT", "POST", "DELETE"]
    allowed_headers = ["*"]
    max_age_seconds = 3000
  }
}

resource "rustfs_quota" "data" {
  bucket = rustfs_bucket.data.name
  quota  = 10737418240 # 10 GiB
}

# ---------------------------------------------------------------------------
# IAM - policy, group and user
# ---------------------------------------------------------------------------
resource "rustfs_policy" "app_readwrite" {
  name = "app-readwrite"

  statement = [
    {
      effect = "Allow"
      action = [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:ListBucket",
      ]
      resource = [
        "arn:aws:s3:::${rustfs_bucket.data.name}",
        "arn:aws:s3:::${rustfs_bucket.data.name}/*",
      ]
    },
  ]
}

resource "rustfs_group" "developers" {
  name    = "developers"
  status  = "enabled"
  members = ["alice", "bob"]
}

resource "rustfs_group_policy_attachment" "developers" {
  group  = rustfs_group.developers.name
  policy = rustfs_policy.app_readwrite.name
}

resource "rustfs_user" "app" {
  access_key = "app-service"
  secret_key = "change-me-in-production"
  status     = "enabled"
  policy     = rustfs_policy.app_readwrite.name
}

resource "rustfs_user_policy_attachment" "app" {
  user   = rustfs_user.app.access_key
  policy = rustfs_policy.app_readwrite.name
}

# ---------------------------------------------------------------------------
# SERVICE ACCOUNT - scoped credential for a CI pipeline
# ---------------------------------------------------------------------------
resource "rustfs_serviceaccount" "ci" {
  access_key  = "ci-bot"
  secret_key  = "change-me-in-production-too"
  name        = "CI Pipeline"
  description = "Token for CI/CD access to app-data"
  expiration  = "2030-01-01T00:00:00Z"
  user        = rustfs_user.app.access_key

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:PutObject"]
        Resource = ["arn:aws:s3:::${rustfs_bucket.data.name}/*"]
      }
    ]
  })
}

# ---------------------------------------------------------------------------
# DATA SOURCES - read the resulting state back
# ---------------------------------------------------------------------------
data "rustfs_users" "all" {
  depends_on = [rustfs_user.app]
}

data "rustfs_iam_policies" "all" {
  depends_on = [rustfs_policy.app_readwrite]
}

data "rustfs_iam_policy" "app_readwrite" {
  name = rustfs_policy.app_readwrite.name
}

data "rustfs_pools" "all" {}

data "rustfs_quota" "data" {
  bucket = rustfs_bucket.data.name
}

output "bucket_name" {
  value = rustfs_bucket.data.name
}

output "user_access_keys" {
  value = data.rustfs_users.all.access_keys
}

output "quota_bytes" {
  value = data.rustfs_quota.data.quota
}
