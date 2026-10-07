terraform {
  required_providers {
    rustfs = {
      source = "weinmann-emt/rustfs"
    }
  }
}

provider "rustfs" {
  # endpoint can also be set via RUSTFS_ENDPOINT env var
  endpoint = "127.0.0.1:9001"
  # access_key can also be set via RUSTFS_USER env var
  access_key = "admin"
  # secret_key can also be set via RUSTFS_SECRET env var
  secret_key = var.rustfs_secret
}
