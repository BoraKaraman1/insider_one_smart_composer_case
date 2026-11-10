package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB runs queries. *pgxpool.Pool, *pgx.Conn and pgx.Tx satisfy it.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Campaign is a campaigns row.
type Campaign struct {
	ID                 string     `json:"id"`
	Brief              string     `json:"brief"`
	State              string     `json:"state"`
	Version            int        `json:"version"`
	CampaignURL        *string    `json:"campaign_url"`
	CurrentRevisionID  *string    `json:"current_revision_id"`
	ApprovedRevisionID *string    `json:"approved_revision_id"`
	ApprovedBy         *string    `json:"approved_by"`
	ApprovedAt         *time.Time `json:"approved_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

const campaignCols = `id::text, brief, state::text, version, campaign_url, current_revision_id::text,
	approved_revision_id::text, approved_by, approved_at, created_at, updated_at`

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	err := row.Scan(&c.ID, &c.Brief, &c.State, &c.Version, &c.CampaignURL, &c.CurrentRevisionID,
		&c.ApprovedRevisionID, &c.ApprovedBy, &c.ApprovedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	return c, err
}

// CreateCampaign inserts a draft campaign.
func CreateCampaign(ctx context.Context, db DB, brief, campaignURL string) (Campaign, error) {
	return scanCampaign(db.QueryRow(ctx, `INSERT INTO campaigns (brief, campaign_url) VALUES ($1, $2) RETURNING `+campaignCols,
		brief, campaignURL))
}

// GetCampaign reads one campaign, or returns ErrNotFound.
func GetCampaign(ctx context.Context, db DB, id string) (Campaign, error) {
	return scanCampaign(db.QueryRow(ctx, `SELECT `+campaignCols+` FROM campaigns WHERE id = $1`, id))
}

// UpdateCampaignState is the guarded update of rule 7. It reports false when
// the campaign is not in state from at version, or does not exist.
func UpdateCampaignState(ctx context.Context, db DB, id, from, to string, version int) (bool, error) {
	tag, err := db.Exec(ctx, `UPDATE campaigns SET state = $2, version = version + 1, updated_at = now()
		WHERE id = $1 AND state = $3 AND version = $4`, id, to, from, version)
	return tag.RowsAffected() == 1, err
}

// InsertCampaignEvent writes one audit row. An empty from is stored as NULL
// (the campaign was just created).
func InsertCampaignEvent(ctx context.Context, db DB, id, from, to, actor, reason string) error {
	_, err := db.Exec(ctx, `INSERT INTO campaign_events (campaign_id, from_state, to_state, actor, reason)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5)`, id, from, to, actor, reason)
	return err
}

// Event is a campaign_events row.
type Event struct {
	From   *string   `json:"from_state"`
	To     string    `json:"to_state"`
	Actor  string    `json:"actor"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// ListCampaignEvents returns a campaign's audit trail, oldest first.
func ListCampaignEvents(ctx context.Context, db DB, id string) ([]Event, error) {
	rows, err := db.Query(ctx, `SELECT from_state::text, to_state::text, actor, reason, at
		FROM campaign_events WHERE campaign_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		err := r.Scan(&e.From, &e.To, &e.Actor, &e.Reason, &e.At)
		return e, err
	})
}

// SkipQueuedMessages takes a cancelled campaign's queued messages out of the
// queue. Messages being sent or already final are left alone.
func SkipQueuedMessages(ctx context.Context, db DB, campaignID string) (int64, error) {
	tag, err := db.Exec(ctx, `UPDATE messages SET state = 'skipped', last_error_class = 'cancelled', updated_at = now()
		WHERE campaign_id = $1 AND state = 'queued'`, campaignID)
	return tag.RowsAffected(), err
}

// Revision is a campaign_revisions row.
type Revision struct {
	ID            string          `json:"id"`
	CampaignID    string          `json:"campaign_id"`
	N             int             `json:"n"`
	Source        string          `json:"source"`
	Segment       json.RawMessage `json:"segment"`
	SMSBody       string          `json:"sms_body"`
	EmailSubject  string          `json:"email_subject"`
	EmailBody     string          `json:"email_body"`
	Warnings      json.RawMessage `json:"warnings"`
	AudienceTotal *int            `json:"audience_total"`
	SMSEligible   *int            `json:"sms_eligible"`
	EmailEligible *int            `json:"email_eligible"`
	CreatedAt     time.Time       `json:"created_at"`
}

const revisionCols = `id::text, campaign_id::text, n, source, segment, sms_body, email_subject, email_body,
	warnings, audience_total, sms_eligible, email_eligible, created_at`

func scanRevision(row pgx.Row) (Revision, error) {
	var r Revision
	err := row.Scan(&r.ID, &r.CampaignID, &r.N, &r.Source, &r.Segment, &r.SMSBody, &r.EmailSubject, &r.EmailBody,
		&r.Warnings, &r.AudienceTotal, &r.SMSEligible, &r.EmailEligible, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	return r, err
}

// GetRevision reads one revision of a campaign, or returns ErrNotFound.
func GetRevision(ctx context.Context, db DB, campaignID, id string) (Revision, error) {
	return scanRevision(db.QueryRow(ctx, `SELECT `+revisionCols+` FROM campaign_revisions WHERE campaign_id = $1 AND id = $2`,
		campaignID, id))
}

// NewRevision is a validated revision to store.
type NewRevision struct {
	CampaignID    string
	Source        string // "llm" or "human"
	Segment       []byte // normalized rule JSON
	SMSBody       string
	EmailSubject  string
	EmailBody     string
	Warnings      []byte // JSON array of issues
	AudienceTotal int
	SMSEligible   int
	EmailEligible int
}

// InsertRevision stores the next revision of a campaign and makes it the
// current one. The caller holds the campaign row lock (a transition in the
// same transaction), so the revision number cannot race.
func InsertRevision(ctx context.Context, db DB, nr NewRevision) (Revision, error) {
	r, err := scanRevision(db.QueryRow(ctx, `INSERT INTO campaign_revisions
		(campaign_id, n, source, segment, sms_body, email_subject, email_body, warnings, audience_total, sms_eligible, email_eligible)
		VALUES ($1, (SELECT coalesce(max(n), 0) + 1 FROM campaign_revisions WHERE campaign_id = $1),
			$2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+revisionCols,
		nr.CampaignID, nr.Source, string(nr.Segment), nr.SMSBody, nr.EmailSubject, nr.EmailBody, string(nr.Warnings),
		nr.AudienceTotal, nr.SMSEligible, nr.EmailEligible))
	if err != nil {
		return Revision{}, err
	}
	_, err = db.Exec(ctx, `UPDATE campaigns SET current_revision_id = $2, updated_at = now() WHERE id = $1`, nr.CampaignID, r.ID)
	return r, err
}
