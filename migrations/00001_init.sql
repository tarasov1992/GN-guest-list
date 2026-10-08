-- +goose Up
CREATE TABLE guests (
    id              integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    surname         text NOT NULL,
    first_name      text NOT NULL DEFAULT '', 
    patronymic      text NOT NULL DEFAULT '',
    priority        text NOT NULL CHECK (priority IN ('regular', 'vip')),
    full_name_key   text NOT NULL UNIQUE,
    checked_in      boolean NOT NULL DEFAULT false,
    plus_one        boolean NOT NULL DEFAULT false CHECK (checked_in OR NOT plus_one), 
    removed         boolean NOT NULL DEFAULT false
);

-- +goose Down
DROP TABLE guests;