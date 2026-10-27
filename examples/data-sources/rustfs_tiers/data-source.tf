# List the storage tiers the server has configured. config_json is the backend
# object with every credential value replaced by REDACTED, so it is safe to
# inspect (and to write to outputs).
data "rustfs_tiers" "all" {}

output "tier_backends" {
  value = [
    for tier in data.rustfs_tiers.all.tiers : {
      name        = tier.name
      tier_type   = tier.tier_type
      config_json = tier.config_json
    }
  ]
}
