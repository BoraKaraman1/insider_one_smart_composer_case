-- Campaigns, their revisions and audit trail, LLM call records, and messages.
-- State lists live in domains so each list is written once. Invariants the
-- state machine and the delivery worker rely on are enforced here too, so a
-- code bug fails loudly instead of corrupting data.

-- +goose Up
CREATE DOMAIN campaign_state AS text CHECK (VALUE IN (
    'draft', 'generating', 'in_review', 'generation_failed', 'approved',
    'sending', 'completed', 'completed_with_failures', 'rejected', 'cancelled'));

CREATE DOMAIN channel AS text CHECK (VALUE IN ('sms', 'email'));

-- "Retrying" is not a state: it is queued with attempts > 0.
CREATE DOMAIN message_state AS text CHECK (VALUE IN (
    'queued', 'sending', 'sent', 'failed', 'dead', 'skipped'));

-- Reason labels, shared by metrics and the dashboard. conn_refused is safe
-- to retry; timeout and conn_reset are ambiguous and safe only because every
-- retry carries the same idempotency key.
CREATE DOMAIN error_class AS text CHECK (VALUE IN (
    'rate_limited', 'server_error', 'timeout', 'conn_refused', 'conn_reset',
    'client_error', 'no_consent', 'cancelled'));

CREATE TABLE campaigns (
    id                   uuid           PRIMARY KEY DEFAULT gen_random_uuid(),
    brief                text           NOT NULL CHECK (char_length(brief) BETWEEN 1 AND 2000),
    state                campaign_state NOT NULL DEFAULT 'draft',
    -- Optimistic locking: every transition is UPDATE ... WHERE version = $v.
    version              integer        NOT NULL DEFAULT 0,
    current_revision_id  uuid,
    approved_revision_id uuid,
    approved_by          text,
    approved_at          timestamptz,
    -- The only source of links in copy ({{campaign_url}}). Code checks it is
    -- an https URL on an allowlisted domain.
    campaign_url         text           CHECK (char_length(campaign_url) <= 40),
    created_at           timestamptz    NOT NULL DEFAULT now(),
    updated_at           timestamptz    NOT NULL DEFAULT now(),
    -- An approval is all or nothing.
    CHECK ((approved_revision_id IS NULL) = (approved_by IS NULL)
       AND (approved_revision_id IS NULL) = (approved_at IS NULL)),
    -- Approved and later states always point at the approved revision.
    CHECK (state NOT IN ('approved', 'sending', 'completed', 'completed_with_failures')
        OR approved_revision_id IS NOT NULL)
);

-- Only valid revisions are stored: an LLM attempt or a human edit becomes a
-- revision after it passes validation (rule 7). Failed LLM attempts are kept
-- in llm_calls; rejected edits are answered with 422 and not stored.
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
    -- Target of the composite foreign keys below.
    UNIQUE (id, campaign_id)
);

-- A campaign can only point at, or approve, its own revisions.
ALTER TABLE campaigns
    ADD FOREIGN KEY (current_revision_id, id) REFERENCES campaign_revisions (id, campaign_id),
    ADD FOREIGN KEY (approved_revision_id, id) REFERENCES campaign_revisions (id, campaign_id);

-- Audit trail: one row per state transition.
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

-- One row per call to Claude, valid or not.
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

-- One row per recipient per channel. The id is the idempotency key sent to
-- the provider on every attempt (rule 10).
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
    -- The same person never gets the same campaign twice on one channel.
    UNIQUE (campaign_id, user_id, channel),
    -- The message belongs to a revision of its own campaign.
    FOREIGN KEY (revision_id, campaign_id) REFERENCES campaign_revisions (id, campaign_id),
    -- A sent message always has the provider's id and a send time.
    CHECK (state <> 'sent' OR (provider_message_id IS NOT NULL AND sent_at IS NOT NULL)),
    -- Failed, dead and skipped messages always say why.
    CHECK (state NOT IN ('failed', 'dead', 'skipped') OR last_error_class IS NOT NULL)
);
CREATE INDEX messages_campaign_state ON messages (campaign_id, state);
-- The dashboard's "Needs attention" panel scans unfinished messages.
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
