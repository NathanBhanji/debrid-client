-- +goose Up
ALTER TABLE torrents ADD COLUMN organized INTEGER NOT NULL DEFAULT 0;
ALTER TABLE torrents ADD COLUMN tracked_paths TEXT NOT NULL DEFAULT '';
ALTER TABLE downloads ADD COLUMN extracted_paths TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE downloads DROP COLUMN extracted_paths;
ALTER TABLE torrents DROP COLUMN tracked_paths;
ALTER TABLE torrents DROP COLUMN organized;
