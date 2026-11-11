# A table bucket is an existing bucket first; see the rustfs_bucket example.
resource "rustfs_bucket" "warehouse" {
  name = "lakehouse"
}

# Enabling the catalog claims the bucket's warehouse location. A scoped
# (non-root) credential needs admin:ClaimTableLocation on the location's
# canonical physical prefix -- here arn:aws:s3:::lakehouse/owned
# (arn:aws:s3:::lakehouse/owned/* covers descendants) -- in addition to
# admin:CreateTable, and the caller must own that location. Without the claim
# the apply fails with AccessDenied; root credentials satisfy every grant.
resource "rustfs_table_bucket" "warehouse" {
  bucket     = rustfs_bucket.warehouse.name
  depends_on = [rustfs_bucket.warehouse]
}

output "catalog_uri" {
  value = rustfs_table_bucket.warehouse.catalog_uri
}
