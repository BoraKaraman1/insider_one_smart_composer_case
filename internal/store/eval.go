package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The eval harness (internal/eval) works on its own clone of a seeded
// template database. Its SQL lives here.

// templateCloneLock is the advisory lock key internal/testdb takes around a
// template clone. Postgres refuses to copy a template while another session
// uses it, so every cloner takes the same lock. Keep the two equal.
const templateCloneLock int64 = 0x7e57db

// CloneDatabase creates database name as a copy of template, over the admin
// connection adminURL. Both names are quoted as identifiers.
func CloneDatabase(ctx context.Context, adminURL, template, name string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", templateCloneLock); err != nil {
		return err
	}
	_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize())
	_, uerr := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", templateCloneLock)
	return errors.Join(err, uerr)
}

// DropDatabase drops database name, closing any session still on it.
func DropDatabase(ctx context.Context, adminURL, name string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	_, err = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	return err
}

// EvalUser is a fixture recipient for the eval harness.
type EvalUser struct {
	FirstName, LastName, Email, Phone string
	City, Language, LoyaltyTier       string
	SignupAt                          time.Time
	ConsentSMS, ConsentEmail          bool
}

// InsertEvalUser inserts one user and returns its id.
func InsertEvalUser(ctx context.Context, db DB, u EvalUser) (string, error) {
	var id string
	err := db.QueryRow(ctx, `INSERT INTO users (first_name, last_name, email, phone, city, language, loyalty_tier,
			signup_at, consent_sms, consent_email)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id::text`,
		u.FirstName, u.LastName, u.Email, u.Phone, u.City, u.Language, u.LoyaltyTier, u.SignupAt, u.ConsentSMS, u.ConsentEmail).Scan(&id)
	return id, err
}

// InsertUserEvent records one event of a user.
func InsertUserEvent(ctx context.Context, db DB, userID, typ string, at time.Time) error {
	_, err := db.Exec(ctx, `INSERT INTO user_events (user_id, type, occurred_at) VALUES ($1, $2, $3)`, userID, typ, at)
	return err
}

// AudienceIDs runs a recipient query from segment.AudienceQuery and returns
// the user ids. The SQL text comes only from the segment compiler.
func AudienceIDs(ctx context.Context, db DB, audienceSQL string, args []any) ([]string, error) {
	rows, err := db.Query(ctx, "SELECT id::text FROM ("+audienceSQL+") a", args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// LLMCallRow is the part of an llm_calls row the eval report reads.
type LLMCallRow struct {
	CampaignID   string
	Stage        string
	Outcome      string
	ErrorClass   string
	LatencyMS    int
	InputTokens  int
	OutputTokens int
	Codes        []string // validation error codes, in order
}

// ListLLMCalls returns a campaign's llm_calls rows in id order.
func ListLLMCalls(ctx context.Context, db DB, campaignID string) ([]LLMCallRow, error) {
	rows, err := db.Query(ctx, `SELECT campaign_id::text, stage, outcome, coalesce(error_class::text, ''), latency_ms,
			coalesce(input_tokens, 0), coalesce(output_tokens, 0), validation_errors
		FROM llm_calls WHERE campaign_id = $1 ORDER BY id`, campaignID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (LLMCallRow, error) {
		var c LLMCallRow
		var errs []byte
		if err := r.Scan(&c.CampaignID, &c.Stage, &c.Outcome, &c.ErrorClass, &c.LatencyMS,
			&c.InputTokens, &c.OutputTokens, &errs); err != nil {
			return c, err
		}
		var issues []struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(errs, &issues); err != nil {
			return c, err
		}
		for _, is := range issues {
			c.Codes = append(c.Codes, is.Code)
		}
		return c, nil
	})
}
