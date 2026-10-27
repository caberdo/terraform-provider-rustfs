# Read the group first: rustfs_site_replication_status is the surface that
# reports each peer's deployment_id, which a resync needs to identify its
# target. The data source is read-only, so reading it is always safe.
data "rustfs_site_replication_status" "group" {
}

# Inspect the peer's resync without starting one. `status` never moves data;
# `start` re-copies the replicated buckets to the peer and `cancel` stops an
# active resync, both of which change replication state and should be applied
# deliberately.
resource "rustfs_site_replication_resync" "status" {
  operation = "status"

  peer = {
    name = one([
      for site in data.rustfs_site_replication_status.group.sites : site.name
      if site.name == "site-b"
    ])
    deployment_id = one([
      for site in data.rustfs_site_replication_status.group.sites : site.deployment_id
      if site.name == "site-b"
    ])
  }
}

output "site_replication_resync" {
  value = {
    op_type = rustfs_site_replication_resync.status.op_type
    status  = rustfs_site_replication_resync.status.status
    state   = rustfs_site_replication_resync.status.state
    message = rustfs_site_replication_resync.status.message
  }
}
