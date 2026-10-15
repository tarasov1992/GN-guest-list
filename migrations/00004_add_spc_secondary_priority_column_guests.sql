-- +goose Up
ALTER TABLE guests
    ADD COLUMN spc_tag boolean NOT NULL DEFAULT false;
-- +goose Down
ALTER TABLE guests DROP COLUMN spc_tag;