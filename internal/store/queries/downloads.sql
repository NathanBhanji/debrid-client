-- name: InsertDownload :execrows
INSERT INTO downloads (
    id, torrent_id, file_id, provider_link, direct_url, rel_path, filename, size, bytes_done, state, error, retry_count, extracted_paths,
    queued_at, started_at, finished_at, unpack_started_at, unpack_finished_at, completed_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(torrent_id, provider_link) DO NOTHING;

-- name: GetDownload :one
SELECT * FROM downloads WHERE id = ?;

-- name: ListDownloadsForTorrent :many
SELECT * FROM downloads WHERE torrent_id = ? ORDER BY rel_path;

-- name: ListDownloadsByState :many
SELECT * FROM downloads WHERE state = ? ORDER BY queued_at;

-- name: UpdateDownload :exec
UPDATE downloads SET
    direct_url = ?, rel_path = ?, filename = ?, size = ?, bytes_done = ?, state = ?, error = ?, retry_count = ?, extracted_paths = ?,
    started_at = ?, finished_at = ?, unpack_started_at = ?, unpack_finished_at = ?, completed_at = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateDownloadProgress :exec
UPDATE downloads SET bytes_done = ?, updated_at = ? WHERE id = ?;

-- name: DeleteDownloadsForTorrent :exec
DELETE FROM downloads WHERE torrent_id = ?;

-- name: CountDownloadsByState :one
SELECT COUNT(*) FROM downloads WHERE state = ?;

-- name: ListDownloads :many
SELECT * FROM downloads ORDER BY queued_at;

-- name: DeleteDownload :exec
DELETE FROM downloads WHERE id = ?;

-- name: CountStartedDownloadsForTorrent :one
SELECT COUNT(*) FROM downloads WHERE torrent_id = ? AND state <> 'pending';
