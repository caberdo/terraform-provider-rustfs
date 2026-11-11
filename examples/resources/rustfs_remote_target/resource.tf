resource "rustfs_bucket" "source" {
  name = "source-bucket"
}

resource "rustfs_bucket_versioning" "source" {
  bucket = rustfs_bucket.source.name
  status = "Enabled"
}

# A target that RustFS derives from a site-replication peer belongs to that
# peer: its endpoint, credentials and TLS settings follow the site topology, and
# updating them here fails with a diagnostic naming the peer deployment id.
# Change such a target through rustfs_site_replication instead.
resource "rustfs_remote_target" "peer" {
  type          = "replication"
  endpoint      = "https://peer.example.com"
  access_key    = "peeruser"
  secret_key    = var.peer_secret
  secure        = true
  bucket        = rustfs_bucket.source.name
  target_bucket = "backup"
  depends_on    = [rustfs_bucket_versioning.source]
}
