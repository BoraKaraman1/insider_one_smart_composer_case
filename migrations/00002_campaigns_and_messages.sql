
-- +goose Up
CREATE DOMAIN campaign_state AS text CHECK (VALUE IN (
    'draft', 'generating', 'in_review', 'generation_failed', 'approved',
    'sending', 'completed', 'completed_with_failures', 'rejected', 'cancelled'));

CREATE DOMAIN channel AS text CHECK (VALUE IN ('sms', 'email'));

-- Yeniden deneme ayrı bir durum değildir.
CREATE DOMAIN message_state AS text CHECK (VALUE IN (
    'queued', 'sending', 'sent', 'failed', 'dead', 'skipped'));

CREATE DOMAIN error_class AS text CHECK (VALUE IN (
    'rate_limited', 'server_error', 'timeout', 'conn_refused', 'conn_reset',
    'client_error', 'no_consent', 'cancelled'));

CREATE TABLE campaigns (
    id                   uuid           PRIMARY KEY DEFAULT gen_random_uuid(),
    brief                text           NOT NULL CHECK (char_length(brief) BETWEEN 1 AND 2000),
    state                campaign_state NOT NULL DEFAULT 'draft',
    version              integer        NOT NULL DEFAULT 0,
    current_revision_id  uuid,
    approved_revision_id uuid,
    approved_by          text,
    approved_at          timestamptz,
    campaign_url         text           CHECK (char_length(campaign_url) <= 40),
    created_at           timestamptz    NOT NULL DEFAULT now(),
    updated_at           timestamptz    NOT NULL DEFAULT now(),
    CHECK ((approved_revision_id IS NULL) = (approved_by IS NULL)
       AND (approved_revision_id IS NULL) = (approved_at IS NULL)),
    CHECK (state NOT IN ('approved', 'sending', 'completed', 'completed_with_failures')
        OR approved_revision_id IS NOT NULL)
);

CREATE TABLE campaign_revisions (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id    uuid        NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    n              integer     NOT NULL CHECK (n >= 1),
    source         text        NOT NULL CHECK (source IN ('llm', 'human')),
    segment        jsonb       NOT NULL,
    sms_body       text        NOT NULL,
    email_subject  text        NOT NULL,
    email_body     text        NOT NULL,
    warnings       jsonb       NOT NULL DEFAULT '[]',
    audience_total integer     CHECK (audience_total >= 0),
    sms_eligible   integer     CHECK (sms_eligible >= 0),
    email_eligible integer     CHECK (email_eligible >= 0),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, n),
    UNIQUE (id, campaign_id)
);

ALTER TABLE campaigns
    ADD FOREIGN KEY (current_revision_id, id) REFERENCES campaign_revisions (id, campaign_id),
    ADD FOREIGN KEY (approved_revision_id, id) REFERENCES campaign_revisions (id, campaign_id);

CREATE TABLE campaign_events (
    id          bigint         GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id uuid           NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    from_state  campaign_state,
    to_state    campaign_state NOT NULL,
    actor       text           NOT NULL,
    reason      text           NOT NULL DEFAULT '',
    at          timestamptz    NOT NULL DEFAULT now()
);
CREATE INDEX campaign_events_campaign ON campaign_events (campaign_id, id);

CREATE TABLE llm_calls (
    id                bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id       uuid        NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    stage             text        NOT NULL CHECK (stage IN ('segment', 'content')),
    attempt           integer     NOT NULL CHECK (attempt >= 1),
    outcome           text        NOT NULL CHECK (outcome IN ('valid', 'invalid', 'refusal', 'max_tokens', 'transport_error')),
    error_class       error_class,
    stop_reason       text,
    model             text        NOT NULL,
    input_tokens      integer     CHECK (input_tokens >= 0),
    output_tokens     integer     CHECK (output_tokens >= 0),
    latency_ms        integer     NOT NULL CHECK (latency_ms >= 0),
    raw_output        text,
    validation_errors jsonb       NOT NULL DEFAULT '[]',
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX llm_calls_campaign ON llm_calls (campaign_id, id);

CREATE TABLE messages (
    id                  uuid          PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id         uuid          NOT NULL,
    revision_id         uuid          NOT NULL,
    user_id             uuid          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel             channel       NOT NULL,
    state               message_state NOT NULL DEFAULT 'queued',
    attempts            integer       NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error_class    error_class,
    last_error          text,
    next_attempt_at     timestamptz,
    provider_message_id text,
    sent_at             timestamptz,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    updated_at          timestamptz   NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, user_id, channel),
    FOREIGN KEY (revision_id, campaign_id) REFERENCES campaign_revisions (id, campaign_id),
    CHECK (state <> 'sent' OR (provider_message_id IS NOT NULL AND sent_at IS NOT NULL)),
    CHECK (state NOT IN ('failed', 'dead', 'skipped') OR last_error_class IS NOT NULL)
);
CREATE INDEX messages_campaign_state ON messages (campaign_id, state);
CREATE INDEX messages_unfinished ON messages (state, updated_at) WHERE state IN ('queued', 'sending');

-- +goose Down
DROP TABLE messages;
DROP TABLE llm_calls;
DROP TABLE campaign_events;
ALTER TABLE campaigns DROP COLUMN current_revision_id, DROP COLUMN approved_revision_id;
DROP TABLE campaign_revisions;
DROP TABLE campaigns;
DROP DOMAIN error_class;
DROP DOMAIN message_state;
DROP DOMAIN channel;
DROP DOMAIN campaign_state;
