-- Delivery: when a message was first tried. The send worker sets it once,
-- on the first attempt, and declares the message dead 15 minutes later
-- (internal/delivery). It stays empty until then.

-- +goose Up
ALTER TABLE messages ADD COLUMN first_attempt_at timestamptz;

-- +goose Down
ALTER TABLE messages DROP COLUMN first_attempt_at;
