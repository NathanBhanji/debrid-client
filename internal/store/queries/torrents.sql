-- name: InsertTorrent :exec
INSERT INTO torrents (
    id, account_id, hash, name, dir_name, organized, tracked_paths, category, status, status_reason, error, progress, size, speed, seeders,
    provider_id, provider_status, files, settings, payload_kind, payload, retry_count,
    added_at, provider_added_at, provider_ended_at, files_selected_at, completed_at, retry_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTorrent :one
SELECT * FROM torrents WHERE id = ?;

-- name: GetTorrentByHash :one
SELECT * FROM torrents WHERE account_id = ? AND hash = ? ORDER BY added_at DESC LIMIT 1;

-- name: GetTorrentByProviderID :one
SELECT * FROM torrents WHERE account_id = ? AND provider_id = ? ORDER BY added_at DESC LIMIT 1;

-- name: ListTorrents :many
SELECT * FROM torrents ORDER BY added_at DESC;

-- name: ListTorrentsByAccount :many
SELECT * FROM torrents WHERE account_id = ? ORDER BY added_at DESC;

-- name: ListTorrentsByStatus :many
SELECT * FROM torrents WHERE status = ? ORDER BY added_at;

-- name: ListActiveTorrents :many
SELECT * FROM torrents WHERE completed_at IS NULL ORDER BY added_at;

-- name: UpdateTorrent :exec
UPDATE torrents SET
    name = ?, dir_name = ?, organized = ?, tracked_paths = ?, category = ?, status = ?, status_reason = ?, error = ?, progress = ?, size = ?, speed = ?, seeders = ?,
    provider_id = ?, provider_status = ?, files = ?, settings = ?, retry_count = ?,
    provider_added_at = ?, provider_ended_at = ?, files_selected_at = ?, completed_at = ?, retry_at = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteTorrent :exec
DELETE FROM torrents WHERE id = ?;

-- name: CountTorrents :one
SELECT COUNT(*) FROM torrents;
