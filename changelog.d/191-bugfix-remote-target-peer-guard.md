`rustfs_remote_target` now explains peer-owned site-replication targets. When a
target records a site-replication peer deployment id (or carries an ARN derived
from the topology), an in-place endpoint, credential or TLS change fails with a
diagnostic naming the peer and pointing at `rustfs_site_replication`, instead of
surfacing the raw RustFS 1.0.5 `400 InvalidRequest`. Importing such a target
emits the same explanation as a warning. Ordinary remote targets keep updating
in place.
