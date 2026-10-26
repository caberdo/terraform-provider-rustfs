# List every bucket the deployment holds, sorted by name. One ListBuckets call
# serves the whole list.
data "rustfs_buckets" "all" {}

output "bucket_names" {
  value = [for bucket in data.rustfs_buckets.all.buckets : bucket.name]
}
