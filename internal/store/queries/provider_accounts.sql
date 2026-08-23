-- name: InsertProviderAccount :exec
INSERT INTO provider_accounts (id, kind, name, credentials, enabled, is_default, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetProviderAccount :one
SELECT * FROM provider_accounts WHERE id = ?;

-- name: GetProviderAccountByName :one
SELECT * FROM provider_accounts WHERE name = ?;

-- name: GetDefaultProviderAccount :one
SELECT * FROM provider_accounts WHERE is_default = 1;

-- name: ListProviderAccounts :many
SELECT * FROM provider_accounts ORDER BY created_at;

-- name: UpdateProviderAccount :exec
UPDATE provider_accounts SET name = ?, credentials = ?, enabled = ?, updated_at = ? WHERE id = ?;

-- name: ClearDefaultProviderAccount :exec
UPDATE provider_accounts SET is_default = 0, updated_at = ? WHERE is_default = 1;

-- name: SetDefaultProviderAccount :exec
UPDATE provider_accounts SET is_default = 1, updated_at = ? WHERE id = ?;

-- name: DeleteProviderAccount :exec
DELETE FROM provider_accounts WHERE id = ?;

-- name: CountTorrentsForAccount :one
SELECT COUNT(*) FROM torrents WHERE account_id = ?;
