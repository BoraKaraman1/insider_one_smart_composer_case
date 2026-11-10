package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GenerationEventsChannel is the LISTEN channel of the event log. The
// payload is the campaign id.
const GenerationEventsChannel = "generation_events"

// GenerationEvent is a generation_events row.
type GenerationEvent struct {
	ID         int64           `json:"id"`
	CampaignID string          `json:"campaign_id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

// InsertGenerationEvent appends an event and notifies listeners. Postgres
// delivers the notification only when the transaction commits, so pass a
// transaction to make the event part of a larger change.
func InsertGenerationEvent(ctx context.Context, db DB, campaignID, typ string, payload any) (int64, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := db.QueryRow(ctx, `INSERT INTO generation_events (campaign_id, type, payload) VALUES ($1, $2, $3) RETURNING id`,
		campaignID, typ, string(b)).Scan(&id); err != nil {
		return 0, err
	}
	_, err = db.Exec(ctx, `SELECT pg_notify($1, $2)`, GenerationEventsChannel, campaignID)
	return id, err
}

// ListGenerationEvents returns a campaign's events with an id above after,
// oldest first.
func ListGenerationEvents(ctx context.Context, db DB, campaignID string, after int64) ([]GenerationEvent, error) {
	rows, err := db.Query(ctx, `SELECT id, campaign_id::text, type, payload, created_at FROM generation_events
		WHERE campaign_id = $1 AND id > $2 ORDER BY id`, campaignID, after)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (GenerationEvent, error) {
		var e GenerationEvent
		err := r.Scan(&e.ID, &e.CampaignID, &e.Type, &e.Payload, &e.CreatedAt)
		return e, err
	})
}

// LatestStartedEventID returns the id of a campaign's newest started event.
// ok is false when the campaign never had a generation.
func LatestStartedEventID(ctx context.Context, db DB, campaignID string) (int64, bool, error) {
	var n *int64
	err := db.QueryRow(ctx, `SELECT max(id) FROM generation_events WHERE campaign_id = $1 AND type = 'started'`,
		campaignID).Scan(&n)
	if err != nil || n == nil {
		return 0, false, err
	}
	return *n, true, nil
}

// LockGenerating locks a campaign row against transitions for the rest of
// the transaction, if the campaign is still generating at version. A
// generation job writes nothing unless this holds, so a cancel or a newer
// generation always wins.
func LockGenerating(ctx context.Context, db DB, id string, version int) (bool, error) {
	var one int
	err := db.QueryRow(ctx, `SELECT 1 FROM campaigns WHERE id = $1 AND state = 'generating' AND version = $2 FOR SHARE`,
		id, version).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// LLMCall is one llm_calls row to insert.
type LLMCall struct {
	CampaignID       string
	Stage            string
	Attempt          int
	Outcome          string // valid, invalid, refusal, max_tokens, transport_error
	ErrorClass       string // empty unless transport_error
	StopReason       string
	Model            string
	InputTokens      *int
	OutputTokens     *int
	LatencyMS        int
	RawOutput        string
	ValidationErrors any // a JSON array
	PromptVersion    string
}

// InsertLLMCall records one call to the model.
func InsertLLMCall(ctx context.Context, db DB, c LLMCall) error {
	errs, err := json.Marshal(c.ValidationErrors)
	if err != nil {
		return err
	}
	if c.ValidationErrors == nil {
		errs = []byte("[]")
	}
	_, err = db.Exec(ctx, `INSERT INTO llm_calls (campaign_id, stage, attempt, outcome, error_class, stop_reason, model,
			input_tokens, output_tokens, latency_ms, raw_output, validation_errors, prompt_version)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8, $9, $10, NULLIF($11, ''), $12, $13)`,
		c.CampaignID, c.Stage, c.Attempt, c.Outcome, c.ErrorClass, c.StopReason, c.Model,
		c.InputTokens, c.OutputTokens, c.LatencyMS, c.RawOutput, string(errs), c.PromptVersion)
	return err
}

// UnfinishedJobs returns the ids of a campaign's River jobs of one kind
// that may still run: queued, scheduled, retryable or running.
func UnfinishedJobs(ctx context.Context, db DB, kind, campaignID string) ([]int64, error) {
	rows, err := db.Query(ctx, `SELECT id FROM river_job WHERE kind = $1 AND args->>'campaign_id' = $2
		AND state NOT IN ('completed', 'cancelled', 'discarded') ORDER BY id`, kind, campaignID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
