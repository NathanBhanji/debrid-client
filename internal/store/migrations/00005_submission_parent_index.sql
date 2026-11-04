-- +goose Up
-- Native retry reviews use a parent-scoped receipt fence. Keep that lookup
-- local to this torrent as retained add/retry history grows.
CREATE INDEX torrent_submissions_torrent_id ON torrent_submissions(torrent_id);

-- +goose Down
DROP INDEX torrent_submissions_torrent_id;
