package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// SetApproval records who approved which revision, while the campaign is
// still in review at version. It reports false when the campaign moved on.
// It runs before the in_review -> approved transition, because the CHECK on
// campaigns wants an approved campaign to name its approved revision.
func SetApproval(ctx context.Context, db DB, id, revisionID, by string, version int) (bool, error) {
	tag, err := db.Exec(ctx, `UPDATE campaigns SET approved_revision_id = $2, approved_by = $3, approved_at = now(), updated_at = now()
		WHERE id = $1 AND state = 'in_review' AND version = $4`, id, revisionID, by, version)
	return tag.RowsAffected() == 1, err
}

// LockCampaign reads a campaign and locks its row for the transaction.
func LockCampaign(ctx context.Context, db DB, id string) (Campaign, error) {
	return scanCampaign(db.QueryRow(ctx, `SELECT `+campaignCols+` FROM campaigns WHERE id = $1 FOR UPDATE`, id))
}

// InsertMessagesFrom inserts one queued message per user id that
// audienceSQL selects, skipping any that already exist, and returns the new
// message ids. audienceSQL comes from segment.AudienceQuery (consent
// included) with its args as $1..$n.
func InsertMessagesFrom(ctx context.Context, db DB, campaignID, revisionID, channel, audienceSQL string, args []any) ([]string, error) {
	n := len(args)
	sql := `INSERT INTO messages (campaign_id, revision_id, user_id, channel)
		SELECT $` + strconv.Itoa(n+1) + `, $` + strconv.Itoa(n+2) + `, a.id, $` + strconv.Itoa(n+3) + `
		FROM (` + audienceSQL + `) a
		ON CONFLICT (campaign_id, user_id, channel) DO NOTHING RETURNING id::text`
	rows, err := db.Query(ctx, sql, append(append([]any{}, args...), campaignID, revisionID, channel)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// CountMessages counts a campaign's messages.
func CountMessages(ctx context.Context, db DB, campaignID string) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE campaign_id = $1`, campaignID).Scan(&n)
	return n, err
}

// Outcome is what a campaign's messages add up to.
type Outcome struct {
	Unfinished int // queued or sending
	Failures   int // failed or dead
}

// MessageOutcome counts a campaign's unfinished and failed messages.
func MessageOutcome(ctx context.Context, db DB, campaignID string) (Outcome, error) {
	var o Outcome
	err := db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state IN ('queued', 'sending')),
			count(*) FILTER (WHERE state IN ('failed', 'dead'))
		FROM messages WHERE campaign_id = $1`, campaignID).Scan(&o.Unfinished, &o.Failures)
	return o, err
}

// Send is everything the send worker needs about one message: its state,
// its campaign, its copy and its recipient. Contact fields go to the
// provider only; they are never logged.
type Send struct {
	ID, CampaignID, Channel, State string
	Attempts                       int
	FirstAttemptAt                 *time.Time
	CampaignState                  string
	CampaignURL                    string
	SMSBody, EmailSubject          string
	EmailBody                      string
	FirstName, LastName, City      string
	Phone, Email                   string
	Consent                        bool // for this message's channel
}

// LoadSend reads a message with its campaign, revision and recipient, or
// returns ErrNotFound.
func LoadSend(ctx context.Context, db DB, id string) (Send, error) {
	var s Send
	err := db.QueryRow(ctx, `SELECT m.id::text, m.campaign_id::text, m.channel::text, m.state::text, m.attempts, m.first_attempt_at,
			c.state::text, coalesce(c.campaign_url, ''), r.sms_body, r.email_subject, r.email_body,
			u.first_name, u.last_name, u.city, u.phone, u.email,
			CASE m.channel WHEN 'sms' THEN u.consent_sms ELSE u.consent_email END
		FROM messages m
		JOIN campaigns c ON c.id = m.campaign_id
		JOIN campaign_revisions r ON r.id = m.revision_id
		JOIN users u ON u.id = m.user_id
		WHERE m.id = $1`, id).Scan(&s.ID, &s.CampaignID, &s.Channel, &s.State, &s.Attempts, &s.FirstAttemptAt,
		&s.CampaignState, &s.CampaignURL, &s.SMSBody, &s.EmailSubject, &s.EmailBody,
		&s.FirstName, &s.LastName, &s.City, &s.Phone, &s.Email, &s.Consent)
	if errors.Is(err, pgx.ErrNoRows) {
		return Send{}, ErrNotFound
	}
	return s, err
}

// Every message update below is guarded by the states it may come from, so
// a late or duplicate run cannot move a message backwards. Each reports
// whether it applied.

func applied(tag interface{ RowsAffected() int64 }, err error) (bool, error) {
	return err == nil && tag.RowsAffected() == 1, err
}

// SkipMessage marks a queued (or reclaimed sending) message skipped.
func SkipMessage(ctx context.Context, db DB, id, class string) (bool, error) {
	return applied(db.Exec(ctx, `UPDATE messages SET state = 'skipped', last_error_class = $2, next_attempt_at = NULL, updated_at = now()
		WHERE id = $1 AND state IN ('queued', 'sending')`, id, class))
}

// ClaimMessage moves a message to sending for one attempt and counts it. A
// message in sending may be claimed again: its last run crashed. It returns
// the attempt number and the first attempt time, or ok false.
func ClaimMessage(ctx context.Context, db DB, id string, now time.Time) (attempts int, first time.Time, ok bool, err error) {
	err = db.QueryRow(ctx, `UPDATE messages SET state = 'sending', attempts = attempts + 1,
			first_attempt_at = coalesce(first_attempt_at, $2), next_attempt_at = NULL, updated_at = now()
		WHERE id = $1 AND state IN ('queued', 'sending') RETURNING attempts, first_attempt_at`, id, now).Scan(&attempts, &first)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, false, nil
	}
	return attempts, first, err == nil, err
}

// MarkSent records the provider's id.
func MarkSent(ctx context.Context, db DB, id, providerID string, at time.Time) (bool, error) {
	return applied(db.Exec(ctx, `UPDATE messages SET state = 'sent', provider_message_id = $2, sent_at = $3, updated_at = now()
		WHERE id = $1 AND state = 'sending'`, id, providerID, at))
}

// MarkRetry puts a message back in the queue until next.
func MarkRetry(ctx context.Context, db DB, id, class, lastError string, next time.Time) (bool, error) {
	return applied(db.Exec(ctx, `UPDATE messages SET state = 'queued', last_error_class = $2, last_error = $3, next_attempt_at = $4, updated_at = now()
		WHERE id = $1 AND state = 'sending'`, id, class, lastError, next))
}

// MarkFinal ends a message as failed or dead.
func MarkFinal(ctx context.Context, db DB, id, state, class, lastError string) (bool, error) {
	return applied(db.Exec(ctx, `UPDATE messages SET state = $2, last_error_class = $3, last_error = $4, next_attempt_at = NULL, updated_at = now()
		WHERE id = $1 AND state = 'sending'`, id, state, class, lastError))
}

// MessageStates are the counted states of the stats API. Retrying is
// queued with attempts above 0 and is not counted as queued.
var MessageStates = []string{"queued", "retrying", "sending", "sent", "failed", "dead", "skipped"}

// DeliveryStats counts a campaign's messages per channel and state, with
// every state of both channels present.
func DeliveryStats(ctx context.Context, db DB, campaignID string) (map[string]map[string]int, error) {
	out := map[string]map[string]int{}
	for _, ch := range []string{"sms", "email"} {
		out[ch] = map[string]int{}
		for _, s := range MessageStates {
			out[ch][s] = 0
		}
	}
	rows, err := db.Query(ctx, `SELECT channel::text,
			CASE WHEN state = 'queued' AND attempts > 0 THEN 'retrying' ELSE state::text END, count(*)
		FROM messages WHERE campaign_id = $1 GROUP BY 1, 2`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch, state string
		var n int
		if err := rows.Scan(&ch, &state, &n); err != nil {
			return nil, err
		}
		out[ch][state] = n
	}
	return out, rows.Err()
}

// Attention thresholds (rule 6). Sending for longer than twice the provider
// timeout means the worker probably died.
const (
	StuckSendingAfter = 10 * time.Second
	OverdueAfter      = 30 * time.Second
	ManyAttempts      = 3
)

// AttentionItem is a stuck or failed message, without contact data.
type AttentionItem struct {
	ID             string     `json:"id"`
	Channel        string     `json:"channel"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	Reason         string     `json:"reason"` // stuck_sending, overdue, many_attempts, failed, dead
	LastErrorClass *string    `json:"last_error_class"`
	LastError      *string    `json:"last_error"`
	NextAttemptAt  *time.Time `json:"next_attempt_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Attention lists a campaign's messages that need a look, as of now.
func Attention(ctx context.Context, db DB, campaignID string, now time.Time) ([]AttentionItem, error) {
	rows, err := db.Query(ctx, `SELECT id::text, channel::text, state::text, attempts,
			CASE
				WHEN state = 'sending' THEN 'stuck_sending'
				WHEN state = 'queued' AND next_attempt_at < $2::timestamptz - $3::interval THEN 'overdue'
				WHEN state = 'queued' THEN 'many_attempts'
				ELSE state::text
			END,
			last_error_class::text, last_error, next_attempt_at, updated_at
		FROM messages
		WHERE campaign_id = $1 AND (
			(state = 'sending' AND updated_at < $2::timestamptz - $4::interval)
			OR (state = 'queued' AND (attempts >= $5 OR next_attempt_at < $2::timestamptz - $3::interval))
			OR state IN ('failed', 'dead'))
		ORDER BY updated_at, id`, campaignID, now, OverdueAfter, StuckSendingAfter, ManyAttempts)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AttentionItem, error) {
		var a AttentionItem
		err := r.Scan(&a.ID, &a.Channel, &a.State, &a.Attempts, &a.Reason, &a.LastErrorClass, &a.LastError, &a.NextAttemptAt, &a.UpdatedAt)
		return a, err
	})
}
