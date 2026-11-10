
-- +goose Up
CREATE TABLE generation_events (
    id          bigserial   PRIMARY KEY,
    campaign_id uuid        NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    type        text        NOT NULL CHECK (type IN (
        'started', 'segment_ready', 'delta', 'attempt_failed', 'done', 'failed')),
    payload     jsonb       NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX generation_events_campaign ON generation_events (campaign_id, id);

ALTER TABLE llm_calls ADD COLUMN prompt_version text;

-- +goose Down
ALTER TABLE llm_calls DROP COLUMN prompt_version;
DROP TABLE generation_events;
