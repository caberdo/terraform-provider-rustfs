# List the IAM service accounts the server persists. This is not the STS
# temporary-key surface: data.rustfs_temporary_accounts lists those.
data "rustfs_service_accounts" "all" {}

output "service_account_keys" {
  value = [
    for account in data.rustfs_service_accounts.all.accounts : {
      access_key  = account.access_key
      parent_user = account.parent_user
      status      = account.account_status
    }
  ]
}
