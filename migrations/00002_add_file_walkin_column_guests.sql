-- +goose Up
ALTER TABLE guests
    ADD COLUMN source text NOT NULL DEFAULT 'file'
    CHECK (source IN ('file', 'walkin'));

-- +goose Down
ALTER TABLE guests DROP COLUMN source;