package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func seededPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	url, _ := testdb.Seeded(t)
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p, url
}

func newCampaignID(t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := p.QueryRow(context.Background(), `INSERT INTO campaigns (brief) VALUES ('b') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestGenerationEventLog(t *testing.T) {
	p, _ := seededPool(t)
	ctx := context.Background()
	a, b := newCampaignID(t, p), newCampaignID(t, p)
	payloads := []map[string]any{{"n": 1.0}, {"field": "sms.body", "text": "Mer"}, {"revision_id": "r"}}
	types := []string{"segment_ready", "delta", "done"}
	var ids []int64
	for i := range types {
		id, err := InsertGenerationEvent(ctx, p, a, types[i], payloads[i])
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if _, err := InsertGenerationEvent(ctx, p, b, "delta", map[string]any{"other": true}); err != nil {
			t.Fatal(err)
		}
	}
	if !(ids[0] < ids[1] && ids[1] < ids[2]) {
		t.Fatalf("ids %v are not increasing", ids)
	}

	all, err := ListGenerationEvents(ctx, p, a, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("%d events, want 3 (another campaign's events must not show)", len(all))
	}
	for i, e := range all {
		var got map[string]any
		if err := json.Unmarshal(e.Payload, &got); err != nil {
			t.Fatal(err)
		}
		if e.ID != ids[i] || e.CampaignID != a || e.Type != types[i] || !reflect.DeepEqual(got, payloads[i]) || e.CreatedAt.IsZero() {
			t.Errorf("event %d: %+v (payload %v)", i, e, got)
		}
	}
	after, err := ListGenerationEvents(ctx, p, a, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].ID != ids[1] || after[1].ID != ids[2] {
		t.Errorf("after %d: %+v, want the last two", ids[0], after)
	}
	if rest, err := ListGenerationEvents(ctx, p, a, ids[2]); err != nil || len(rest) != 0 {
		t.Errorf("after the last id: %v %v, want none", rest, err)
	}
}

func TestGenerationEventNotify(t *testing.T) {
	p, url := seededPool(t)
	ctx := context.Background()
	id := newCampaignID(t, p)

	listener, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close(ctx)
	if _, err := listener.Exec(ctx, "LISTEN "+GenerationEventsChannel); err != nil {
		t.Fatal(err)
	}
	if GenerationEventsChannel != "generation_events" {
		t.Errorf("channel %q, want generation_events", GenerationEventsChannel)
	}

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := InsertGenerationEvent(ctx, tx, id, "delta", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	wait := func(d time.Duration) (string, error) {
		wctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		n, err := listener.WaitForNotification(wctx)
		if err != nil {
			return "", err
		}
		return n.Channel + " " + n.Payload, nil
	}
	if got, err := wait(200 * time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("notified before commit: %q %v", got, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := wait(5 * time.Second)
	if err != nil || got != "generation_events "+id {
		t.Errorf("after commit: %q %v, want %q", got, err, "generation_events "+id)
	}
}

func TestLLMCallsPromptVersion(t *testing.T) {
	p, _ := seededPool(t)
	ctx := context.Background()
	id := newCampaignID(t, p)
	var got string
	err := p.QueryRow(ctx, `INSERT INTO llm_calls (campaign_id, stage, attempt, outcome, model, latency_ms, prompt_version)
		VALUES ($1, 'segment', 1, 'valid', 'claude-opus-5-5', 5, 'segment-v1') RETURNING prompt_version`, id).Scan(&got)
	if err != nil || got != "segment-v1" {
		t.Errorf("prompt_version %q, %v", got, err)
	}
}
