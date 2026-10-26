# A basic bucket with tags and a region set at creation time.
resource "rustfs_bucket" "example" {
  name             = "my-bucket"
  object_ownership = "BucketOwnerEnforced"

  create_bucket_configuration = {
    location_constraint = "eu-west-1"

    tags = {
      env = "test"
    }
  }
}

# A bucket created with a canned ACL and legacy grant recipients. Changing the
# ACL or a grant updates the bucket in place with PutBucketAcl. RustFS 1.0.x
# accepts these values but reports only the owner grant back, so Terraform keeps
# the configured value and cannot detect an out-of-band ACL change.
resource "rustfs_bucket" "public" {
  name             = "my-public-bucket"
  acl              = "public-read"
  object_ownership = "BucketOwnerPreferred"

  grant_read         = "uri=http://acs.amazonaws.com/groups/global/AllUsers"
  grant_read_acp     = "uri=http://acs.amazonaws.com/groups/global/AllUsers"
  grant_full_control = "emailaddress=ops@example.com"
}

# A bucket with S3 Object Lock enabled. Object Lock can only be switched on at
# bucket creation time and can never be disabled afterwards.
resource "rustfs_bucket" "locked" {
  name                           = "my-locked-bucket"
  object_lock_enabled_for_bucket = true
}

# A bucket Terraform may destroy while it still holds data. force_destroy
# deletes every object, object version and incomplete multipart upload first;
# without it a non-empty bucket fails with BucketNotEmpty.
resource "rustfs_bucket" "scratch" {
  name          = "my-scratch-bucket"
  force_destroy = true
}