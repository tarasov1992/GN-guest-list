-- +goose Up
CREATE table student_count (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    total integer NOT NULL DEFAULT 0 CHECK (total >= 0)
);
INSERT INTO student_count DEFAULT VALUES;

-- +goose Down
DROP table student_count;