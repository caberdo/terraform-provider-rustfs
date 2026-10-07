variable "rustfs_secret" {
  description = "Secret key for the RustFS user. Prefer setting RUSTFS_SECRET in the environment."
  type        = string
  sensitive   = true
}
