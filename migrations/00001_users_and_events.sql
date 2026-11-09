-- Recipients and their events: the data a segment rule selects from.
-- Every catalog column is NOT NULL, so neq / not_in never meet SQL's
-- three-valued logic (docs/segment-rules.md, Semantics). The CHECK lists
-- repeat the Go catalog (internal/segment/catalog.go); a test fails if they
-- drift apart. No password, token or secret columns exist anywhere (rule 9).

-- +goose Up
CREATE TABLE users (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    first_name    text        NOT NULL DEFAULT '',
    last_name     text        NOT NULL,
    email         text        NOT NULL UNIQUE,
    phone         text        NOT NULL,
    city          text        NOT NULL CHECK (city IN ('istanbul', 'ankara', 'izmir', 'bursa', 'antalya', 'adana', 'konya', 'gaziantep', 'kocaeli', 'eskisehir')),
    language      text        NOT NULL CHECK (language IN ('tr', 'en')),
    loyalty_tier  text        NOT NULL CHECK (loyalty_tier IN ('bronze', 'silver', 'gold')),
    signup_at     timestamptz NOT NULL,
    consent_sms   boolean     NOT NULL,
    consent_email boolean     NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_events (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type        text        NOT NULL CHECK (type IN ('cart_add', 'purchase', 'page_view')),
    occurred_at timestamptz NOT NULL
);

-- Serves the EXISTS / NOT EXISTS subqueries the compiler emits.
CREATE INDEX user_events_user_type_time ON user_events (user_id, type, occurred_at);

-- The seed's time anchor (docs/seed-data.md). One row at most.
CREATE TABLE seed_meta (
    id         boolean     PRIMARY KEY DEFAULT true CHECK (id),
    as_of      timestamptz NOT NULL,
    seed       bigint      NOT NULL,
    user_count integer     NOT NULL CHECK (user_count >= 0),
    loaded_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE seed_meta;
DROP TABLE user_events;
DROP TABLE users;
