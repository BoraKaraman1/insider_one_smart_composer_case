package delivery

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// İş satırı doğrudan yazılır çünkü River bir işi ancak haftalar süren denemelerden sonra bırakır.
func insertJob(t *testing.T, p *pgxpool.Pool, kind, state, args string) {
	t.Helper()
	if _, err := p.Exec(context.Background(), `INSERT INTO river_job (kind, queue, state, args, max_attempts, attempt, finalized_at)
		VALUES ($1, $1, $2::river_job_state, $3::jsonb, 1, 1, CASE WHEN $2 = 'available' THEN NULL ELSE now() END)`, kind, state, args); err != nil {
		t.Fatal(err)
	}
}

func reconcile(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	// İki kez çalışır ki tekrarın zararsız olduğu her testte görülsün.
	for range 2 {
		if err := Reconcile(context.Background(), p, slog.New(slog.DiscardHandler), noMetrics{}); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
}

func lastError(t *testing.T, p *pgxpool.Pool, id string) string {
	t.Helper()
	var s *string
	if err := p.QueryRow(context.Background(), `SELECT last_error FROM messages WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return str(s)
}

func TestReconcileDiscardedSendJobs(t *testing.T) {
	pool, _ := appPool(t)
	c := insertSendingCampaign(t, pool)
	users := consentingUsers(t, pool, 3)
	queued := insertMessage(t, pool, c, users[0], "sms", "")
	// Sağlayıcı kabul etmiş ama kayıt yazılamamış mesaj gönderimde kalır.
	accepted := insertMessage(t, pool, c, users[1], "sms", "state = 'sending', attempts = 1")
	sent := insertMessage(t, pool, c, users[2], "sms", "state = 'sent', attempts = 1, provider_message_id = 'p1', sent_at = now()")
	for _, id := range []string{queued, accepted, sent} {
		insertJob(t, pool, "send", "discarded", `{"message_id":"`+id+`"}`)
	}
	live := insertSendingCampaign(t, pool)
	waiting := insertMessage(t, pool, live, users[0], "sms", "")
	insertJob(t, pool, "send", "available", `{"message_id":"`+waiting+`"}`)

	reconcile(t, pool)

	if m := loadMessage(t, pool, queued); m.State != "dead" || str(m.LastErrorClass) == "" {
		t.Errorf("queued message of a discarded job: %+v; want dead with an error class", m)
	}
	if m := loadMessage(t, pool, accepted); m.State != "dead" || !strings.Contains(lastError(t, pool, accepted), "unknown") {
		t.Errorf("message left in sending: %+v, %q; want dead and marked as an unknown outcome", m, lastError(t, pool, accepted))
	}
	if m := loadMessage(t, pool, sent); m.State != "sent" {
		t.Errorf("a sent message changed: %+v", m)
	}
	if state, _ := campaignState(t, pool, c.ID); state != "completed_with_failures" {
		t.Errorf("campaign %s, want completed_with_failures", state)
	}
	if m := loadMessage(t, pool, waiting); m.State != "queued" {
		t.Errorf("a message whose job is still live changed: %+v", m)
	}
	if state, _ := campaignState(t, pool, live.ID); state != "sending" {
		t.Errorf("campaign with a live job is %s, want sending", state)
	}
}

func TestReconcileCompletesCampaignWhoseCheckWasLost(t *testing.T) {
	pool, _ := appPool(t)
	c := insertSendingCampaign(t, pool)
	insertMessage(t, pool, c, consentingUsers(t, pool, 1)[0], "sms", "state = 'sent', attempts = 1, provider_message_id = 'p1', sent_at = now()")

	reconcile(t, pool)

	if state, _ := campaignState(t, pool, c.ID); state != "completed" {
		t.Errorf("campaign %s, want completed", state)
	}
}

func TestReconcileDiscardedGeneration(t *testing.T) {
	pool, _ := appPool(t)
	ctx := context.Background()
	stuck := insertCampaignIn(t, pool, "generating", 3)
	insertJob(t, pool, "generate", "discarded", `{"campaign_id":"`+stuck.ID+`","version":3}`)
	// Eski sürümün tükenen işi yeni üretime dokunmamalı.
	newer := insertCampaignIn(t, pool, "generating", 5)
	insertJob(t, pool, "generate", "discarded", `{"campaign_id":"`+newer.ID+`","version":3}`)
	insertJob(t, pool, "generate", "available", `{"campaign_id":"`+newer.ID+`","version":5}`)

	reconcile(t, pool)

	if state, version := campaignState(t, pool, stuck.ID); state != "generation_failed" || version != 4 {
		t.Errorf("campaign %s at version %d, want generation_failed at 4", state, version)
	}
	var reasons []string
	rows, err := pool.Query(ctx, `SELECT payload->>'reason' FROM generation_events WHERE campaign_id = $1 AND type = 'failed'`, stuck.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatal(err)
		}
		reasons = append(reasons, r)
	}
	if !slices.Equal(reasons, []string{"job_exhausted"}) {
		t.Errorf("failed events %q, want exactly one with reason job_exhausted", reasons)
	}
	if state, version := campaignState(t, pool, newer.ID); state != "generating" || version != 5 {
		t.Errorf("newer generation is %s at version %d, want generating at 5", state, version)
	}
}

func TestReconcileFailedFanout(t *testing.T) {
	pool, _ := appPool(t)
	discarded := insertCampaignIn(t, pool, "approved", 2)
	insertJob(t, pool, "fanout", "discarded", `{"campaign_id":"`+discarded.ID+`"}`)
	cancelledJob := insertCampaignIn(t, pool, "approved", 2)
	insertJob(t, pool, "fanout", "cancelled", `{"campaign_id":"`+cancelledJob.ID+`"}`)
	pending := insertCampaignIn(t, pool, "approved", 2)
	insertJob(t, pool, "fanout", "available", `{"campaign_id":"`+pending.ID+`"}`)

	reconcile(t, pool)

	for _, c := range []camp{discarded, cancelledJob} {
		if state, _ := campaignState(t, pool, c.ID); state != "cancelled" {
			t.Errorf("campaign %s, want cancelled", state)
		}
		if trail := auditTrail(t, pool, c.ID); !slices.Contains(trail, "approved>cancelled") {
			t.Errorf("audit trail %q lacks the cancellation", trail)
		}
	}
	if state, _ := campaignState(t, pool, pending.ID); state != "approved" {
		t.Errorf("campaign with a pending fan-out is %s, want approved", state)
	}
}
