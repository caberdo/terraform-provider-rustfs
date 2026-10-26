`rustfs_kms_rekey` now explains a refused start instead of quoting the server:
when RustFS answers `501` because the configured KMS backend cannot re-wrap
envelopes, the diagnostic names the `POST /rustfs/admin/v3/kms/keys/rekey`
route, the backend and the server version, and says which backends can
(`Vault KV2` and `Vault Transit`). `rustfs_kms_backup` checks
`GET /rustfs/admin/v3/kms/backup` before writing: a server that reports
`export_supported = false` now fails the apply with a diagnostic naming
`RUSTFS_KMS_BACKUP_DIR`, `RUSTFS_KMS_BACKUP_KEK` and `RUSTFS_KMS_BACKUP_KEK_ID`
instead of attempting the `POST` and echoing its raw payload.
