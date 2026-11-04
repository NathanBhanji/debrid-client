-- name: GetTorrentSubmission :one
SELECT *, EXISTS(SELECT 1 FROM torrents WHERE id = torrent_submissions.torrent_id) AS torrent_exists
FROM torrent_submissions WHERE key_hash = ?;

-- name: InsertTorrentSubmission :exec
INSERT INTO torrent_submissions (key_hash, request_hash, torrent_id, created_at) VALUES (?, ?, ?, ?);

-- name: CountTorrentSubmissions :one
SELECT COUNT(*) FROM torrent_submissions WHERE torrent_id = ?;
