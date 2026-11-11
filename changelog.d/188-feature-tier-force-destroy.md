`rustfs_tier` gains `force_destroy`. Destroying a tier with `force_destroy = true`
removes the tier record even when RustFS reports the backend as not empty or still in use, and
RustFS 1.0.5 and later can skip the backend probe, so a tier with an unroutable endpoint can be
removed at all. Remote objects are never deleted. A refused destroy now includes the server's
`deletion-check` verdict (`scan_status` and the blocker kinds) on RustFS 1.0.5 or later, and older
servers keep reporting the plain refusal.
