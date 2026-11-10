
-- +goose Up
ALTER TABLE messages ADD COLUMN first_attempt_at timestamptz;

-- +goose Down
ALTER TABLE messages DROP COLUMN first_attempt_at;
