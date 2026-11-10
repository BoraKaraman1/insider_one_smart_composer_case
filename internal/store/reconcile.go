package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Tükenen işin mesajını artık kimse çalıştırmaz, bu yüzden ölü sayılır.
// Gönderimde kalmış mesajın son denemesi bilinmez ve hata metni bunu söyler.
func ExpireDiscardedSends(ctx context.Context, db DB) (channels []string, err error) {
	rows, err := db.Query(ctx, `UPDATE messages m SET state = 'dead',
			last_error_class = coalesce(m.last_error_class, 'timeout'),
			last_error = CASE WHEN m.state = 'sending' THEN 'the send job ran out of retries; the outcome of the last attempt is unknown'
				ELSE 'the send job ran out of retries' END,
			next_attempt_at = NULL, updated_at = now()
		FROM river_job j
		WHERE j.kind = 'send' AND j.state = 'discarded' AND j.args->>'message_id' = m.id::text
			AND m.state IN ('queued', 'sending')
		RETURNING m.channel::text`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func FinishedSendingCampaigns(ctx context.Context, db DB) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT c.id::text FROM campaigns c WHERE c.state = 'sending'
		AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.campaign_id = c.id AND m.state IN ('queued', 'sending'))
		ORDER BY c.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

type Stranded struct {
	CampaignID string
	Version    int
}

func stranded(ctx context.Context, db DB, sql string) ([]Stranded, error) {
	rows, err := db.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Stranded, error) {
		var s Stranded
		err := r.Scan(&s.CampaignID, &s.Version)
		return s, err
	})
}

// Yalnız işin başlattığı sürümde kalan kampanya döner ki yeni üretime dokunulmasın.
func StrandedGenerations(ctx context.Context, db DB) ([]Stranded, error) {
	return stranded(ctx, db, `SELECT DISTINCT c.id::text, c.version FROM river_job j
		JOIN campaigns c ON c.id::text = j.args->>'campaign_id'
		WHERE j.kind = 'generate' AND j.state = 'discarded'
			AND c.state = 'generating' AND c.version::text = j.args->>'version'`)
}

// İptal edilen dağıtım işi de sayılır çünkü işçi bozuk kuralda işi iptal eder.
func StrandedFanouts(ctx context.Context, db DB) ([]Stranded, error) {
	return stranded(ctx, db, `SELECT DISTINCT c.id::text, c.version FROM river_job j
		JOIN campaigns c ON c.id::text = j.args->>'campaign_id'
		WHERE j.kind = 'fanout' AND j.state IN ('discarded', 'cancelled') AND c.state = 'approved'`)
}
