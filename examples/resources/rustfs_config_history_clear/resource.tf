# Destructive and irreversible: every rollback point in the server's config
# history is deleted, so rustfs_config_restore can no longer reach them. confirm
# must be the literal "clear-config-history".
#
# This is an action, not managed state: Read only echoes the recorded result and
# Delete is a no-op, so destroying this resource never deletes history again.
# Change a trigger when you deliberately want to clear the history again.
resource "rustfs_config_history_clear" "purge" {
  confirm = "clear-config-history"

  triggers = {
    incident = "INC-1042"
  }
}
