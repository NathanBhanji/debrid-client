-- +goose Up
CREATE TABLE provider_accounts (
    id          TEXT PRIMARY KEY,                 -- uuid
    kind        TEXT NOT NULL,                    -- torbox|realdebrid|alldebrid|premiumize|debridlink
    name        TEXT NOT NULL UNIQUE,             -- user-facing label
    credentials TEXT NOT NULL,                    -- JSON blob (api key / oauth tokens)
    enabled     INTEGER NOT NULL DEFAULT 1,
    is_default  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE UNIQUE INDEX provider_accounts_default ON provider_accounts(is_default) WHERE is_default = 1;

CREATE TABLE torrents (
    id                  TEXT PRIMARY KEY,         -- uuid
    account_id          TEXT NOT NULL REFERENCES provider_accounts(id) ON DELETE RESTRICT,
    hash                TEXT NOT NULL,            -- lowercase info hash
    name                TEXT NOT NULL DEFAULT '',
    category            TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL,            -- domain.TorrentStatus
    status_reason       TEXT NOT NULL DEFAULT '', -- human-readable why we're in this state
    error               TEXT NOT NULL DEFAULT '',
    progress            REAL NOT NULL DEFAULT 0,  -- provider-side 0..1
    size                INTEGER NOT NULL DEFAULT 0,
    speed               INTEGER NOT NULL DEFAULT 0,
    seeders             INTEGER NOT NULL DEFAULT 0,
    provider_id         TEXT NOT NULL DEFAULT '', -- id at the provider once added
    provider_status     TEXT NOT NULL DEFAULT '', -- raw provider status string
    files               TEXT NOT NULL DEFAULT '[]', -- JSON []domain.File
    settings            TEXT NOT NULL DEFAULT '{}', -- JSON domain.TorrentSettings
    payload_kind        TEXT NOT NULL,            -- magnet|file
    payload             BLOB NOT NULL,            -- magnet URI bytes or .torrent bytes
    retry_count         INTEGER NOT NULL DEFAULT 0,
    added_at            TEXT NOT NULL,
    provider_added_at   TEXT,
    provider_ended_at   TEXT,
    files_selected_at   TEXT,
    completed_at        TEXT,
    retry_at            TEXT,
    updated_at          TEXT NOT NULL
);
CREATE INDEX torrents_account_hash ON torrents(account_id, hash);
CREATE INDEX torrents_status ON torrents(status);
CREATE INDEX torrents_provider_id ON torrents(account_id, provider_id);

CREATE TABLE downloads (
    id                  TEXT PRIMARY KEY,         -- uuid
    torrent_id          TEXT NOT NULL REFERENCES torrents(id) ON DELETE CASCADE,
    file_id             TEXT NOT NULL DEFAULT '', -- provider file id
    provider_link       TEXT NOT NULL,            -- restricted link / file ref, unique per torrent
    direct_url          TEXT NOT NULL DEFAULT '', -- unrestricted URL (expires)
    rel_path            TEXT NOT NULL,            -- path relative to torrent dir
    filename            TEXT NOT NULL,
    size                INTEGER NOT NULL DEFAULT 0,
    bytes_done          INTEGER NOT NULL DEFAULT 0,
    state               TEXT NOT NULL,            -- domain.DownloadState
    error               TEXT NOT NULL DEFAULT '',
    retry_count         INTEGER NOT NULL DEFAULT 0,
    queued_at           TEXT NOT NULL,
    started_at          TEXT,
    finished_at         TEXT,
    unpack_started_at   TEXT,
    unpack_finished_at  TEXT,
    completed_at        TEXT,
    updated_at          TEXT NOT NULL,
    UNIQUE(torrent_id, provider_link)
);
CREATE INDEX downloads_state ON downloads(state);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE settings;
DROP TABLE downloads;
DROP TABLE torrents;
DROP TABLE provider_accounts;
