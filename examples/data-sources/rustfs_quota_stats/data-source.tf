# The quota subsystem's view of one bucket: its configured hard quota and the
# usage it accounted. Unlike rustfs_bucket_usage (the scanner's
# eventually-consistent accounting), this read is what quota admission uses, so
# it can be reservation/ledger based. A field the server cannot report is null.
data "rustfs_quota_stats" "logs" {
  bucket = "logs"
}

output "logs_quota" {
  value = {
    limit      = data.rustfs_quota_stats.logs.quota_limit
    usage      = data.rustfs_quota_stats.logs.current_usage
    remaining  = data.rustfs_quota_stats.logs.remaining_quota
    percentage = data.rustfs_quota_stats.logs.usage_percentage
  }
}
