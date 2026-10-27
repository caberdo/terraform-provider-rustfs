# Read the site-replication group and its peers. No inputs: the data source
# always reports the deployment the provider is configured against.
data "rustfs_site_replication_status" "group" {
}

output "site_replication_enabled" {
  value = data.rustfs_site_replication_status.group.enabled
}

# Peer deployment IDs are what a resync targets, and only this data source
# reports them.
output "site_replication_peers" {
  value = {
    for site in data.rustfs_site_replication_status.group.sites : site.name => {
      endpoint      = site.endpoint
      deployment_id = site.deployment_id
      sync_state    = site.sync_state
    }
  }
}
