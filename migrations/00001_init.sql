-- +goose Up
CREATE TABLE guests (
    id              integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    surname         text NOT NULL,
    first_name      text NOT NULL, 
    patronymic      text NOT NULL DEFAULT '',
    status          text NOT NULL CHECK (status IN ('regular', 'vip')),
    full_name_key   text NOT NULL UNIQUE,
    checked_in      boolean NOT NULL DEFAULT false,
    plus_ones       integer NOT NULL DEFAULT 0 CHECK (plus_ones >= 0),
    removed         boolean NOT NULL DEFAULT false
);

-- +goose Down
DROP TABLE guests;