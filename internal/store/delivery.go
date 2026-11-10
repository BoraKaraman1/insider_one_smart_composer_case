package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Onay alanları durum değişmeden yazılır çünkü kısıt onaylı kampanyada sürüm ister.
func SetApproval(ctx context.Context, db DB, id, revisionID, by string, version int) (bool, error) {
	tag, err := db.Exec(ctx, `UPDATE campaigns SET approved_revision_id = $2, approved_by = $3, approved_at = now(), updated_at = now()
		WHERE id = $1 AND state = 'in_review' AND version = $4`, id, revisionID, by, version)
	return tag.RowsAffected() == 1, err
}

func LockCampaign(ctx context.Context, db DB, id string) (Campaign, error) {
	return scanCampaign(db.QueryRow(ctx, `SELECT `+campaignCols+` FROM campaigns WHERE id = $1 FOR UPDATE`, id))
}

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

func CountMessages(ctx context.Context, db DB, campaignID string) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE campaign_id = $1`, campaignID).Scan(&n)
	return n, err
}

type Outcome struct {
	Unfinished int
	Failures   int
}

func MessageOutcome(ctx context.Context, db DB, campaignID string) (Outcome, error) {
	var o Outcome
	err := db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state IN ('queued', 'sending')),
			count(*) FILTER (WHERE state IN ('failed', 'dead'))
		FROM messages WHERE campaign_id = $1`, campaignID).Scan(&o.Unfinished, &o.Failures)
	return o, err
}

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
	Consent                        bool
}

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

func applied(tag interface{ RowsAffected() int64 }, err error) (bool, error) {
	return err == nil && tag.RowsAffected() == 1, err
}

func SkipMessage(ctx context.Context, db DB, id, class string) (bool, error) {
	return applied(db.Exec(ctx, `UPDATE messages SET state = 'skipped', last_error_class = $2, next_attempt_at = NULL, updated_at = now()
		WHERE id = $1 AND state IN ('queued', 'sending')`, id, class))
}

// Gönderimde kalan mesaj çöken denemeden sonra yeniden alınabilir.
func ClaimMessage(ctx context.Context, db DB, id string, now time.Time) (attempts int, first time.Time, ok bool, err error) {
	err = db.QueryRow(ctx, `UPDATE messages m SET state = 'sending', attempts = m.attempts + 1,
			first_attempt_at = coalesce(m.first_attempt_at, $2), next_attempt_at = NULL, updated_at = now()
		FROM users u, campaigns c
		WHERE m.id = $1 AND m.state IN ('queued', 'sending')
			AND u.id = m.user_id AND c.id = m.campaign_id AND c.state <> 'cancelled'
			AND CASE m.channel WHEN 'sms' THEN u.consent_sms ELSE u.consent_email END
		RETURNING m.attempts, m.first_attempt_at`, id, now).Scan(&attempts, &first)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, false, nil
	}
	return attempts, first, err == nil, err
}

func MarkSent(ctx context.Context, db DB, id, providerID string, at time.Time) (bool, error) {
	return applied(db.Exec(ctx, `UPDATE messages SET state = 'sent', provider_message_id = $2, sent_at = $3, updated_at = now()
		WHERE id = $1 AND state = 'sending'`, id, providerID, at))
}

func MarkRetry(ctx context.Context, db DB, id, class, lastError string, next time.Time) (bool, error) {
	return applied(db.Exec(ctx, `UPDATE messages SET state = 'queued', last_error_class = $2, last_error = $3, next_attempt_at = $4, updated_at = now()
		WHERE id = $1 AND state = 'sending'`, id, class, lastError, next))
}

func MarkFinal(ctx context.Context, db DB, id, state, class, lastError string) (bool, error) {
	return applied(db.Exec(ctx, `UPDATE messages SET state = $2, last_error_class = $3, last_error = $4, next_attempt_at = NULL, updated_at = now()
		WHERE id = $1 AND state = 'sending'`, id, state, class, lastError))
}

var MessageStates = []string{"queued", "retrying", "sending", "sent", "failed", "dead", "skipped"}

func DeliveryStats(ctx context.Context, db DB, campaignID string) (map[string]map[string]int, error) {
	out := map[string]map[string]int{}
	for _, ch := range []string{"sms", "email"} {
		out[ch] = map[string]int{}
		for _, s := range MessageStates {
			out[ch][s] = 0
		}
	}
	// Yeniden denenen kuyrukta durur ama kuyruk sayısına girmez.
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

const (
	StuckSendingAfter = 10 * time.Second
	OverdueAfter      = 30 * time.Second
	NotStartedAfter   = 2 * time.Minute
	ManyAttempts      = 3
)

type AttentionItem struct {
	ID             string     `json:"id"`
	Channel        string     `json:"channel"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	Reason         string     `json:"reason"`
	LastErrorClass *string    `json:"last_error_class"`
	LastError      *string    `json:"last_error"`
	NextAttemptAt  *time.Time `json:"next_attempt_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func Attention(ctx context.Context, db DB, campaignID string, now time.Time) ([]AttentionItem, error) {
	rows, err := db.Query(ctx, `SELECT id::text, channel::text, state::text, attempts,
			CASE
				WHEN state = 'sending' THEN 'stuck_sending'
				WHEN state = 'queued' AND attempts = 0 AND next_attempt_at IS NULL THEN 'not_started'
				WHEN state = 'queued' AND next_attempt_at < $2::timestamptz - $3::interval THEN 'overdue'
				WHEN state = 'queued' THEN 'many_attempts'
				ELSE state::text
			END,
			last_error_class::text, last_error, next_attempt_at, updated_at
		FROM messages
		WHERE campaign_id = $1 AND (
			(state = 'sending' AND updated_at < $2::timestamptz - $4::interval)
			OR (state = 'queued' AND attempts = 0 AND next_attempt_at IS NULL AND created_at < $2::timestamptz - $6::interval)
			OR (state = 'queued' AND (attempts >= $5 OR next_attempt_at < $2::timestamptz - $3::interval))
			OR state IN ('failed', 'dead'))
		ORDER BY updated_at, id`, campaignID, now, OverdueAfter, StuckSendingAfter, ManyAttempts, NotStartedAfter)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AttentionItem, error) {
		var a AttentionItem
		err := r.Scan(&a.ID, &a.Channel, &a.State, &a.Attempts, &a.Reason, &a.LastErrorClass, &a.LastError, &a.NextAttemptAt, &a.UpdatedAt)
		return a, err
	})
}
