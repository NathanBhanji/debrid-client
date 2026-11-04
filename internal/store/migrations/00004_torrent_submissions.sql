-- +goose Up
-- Deliberately no foreign key: accepted submissions survive torrent/account deletion.
CREATE TABLE torrent_submissions (
    key_hash TEXT PRIMARY KEY NOT NULL CHECK(length(key_hash) = 64),
    request_hash TEXT NOT NULL CHECK(length(request_hash) = 64),
    torrent_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE torrent_submissions;
