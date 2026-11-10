package campaign

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockllm"
)

func jobState(t *testing.T, p *pgxpool.Pool, campaignID string) []string {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT state::text FROM river_job WHERE kind = 'generate'
		AND args->>'campaign_id' = $1 ORDER BY id`, campaignID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestCancelCancelsQueuedGenerateJob(t *testing.T) {
	p, _ := genPool(t)
	ctx := context.Background()
	rc, err := river.NewClient(riverpgxv5.New(p), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	draft := func() string {
		id := insertGenerating(t, p, exampleBrief, 0)
		if _, err := p.Exec(ctx, `UPDATE campaigns SET state = 'draft' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id, other := draft(), draft()
	for _, c := range []string{id, other} {
		if _, err := StartGeneration(ctx, p, rc, c, 0, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	if err := CancelWithJobs(ctx, p, rc, id, 1, "admin"); err != nil {
		t.Fatal(err)
	}
	if got := jobState(t, p, id); len(got) != 1 || got[0] != "cancelled" {
		t.Errorf("job states %v, want [cancelled]", got)
	}
	if got := jobState(t, p, other); len(got) != 1 || got[0] != "available" {
		t.Errorf("other campaign's job states %v, want [available]", got)
	}
	events := genEvents(t, p, id)
	if last := events[len(events)-1]; last.Type != "failed" || last.Payload["reason"] != ReasonCancelled {
		t.Errorf("last event %+v, want failed with reason cancelled", last)
	}
	if err := Cancel(ctx, p, other, 1, "admin"); err != nil {
		t.Fatal(err)
	}
	if got := jobState(t, p, other); got[0] != "available" {
		t.Errorf("plain Cancel touched the job: %v", got)
	}
}

func TestCancelStopsRunningGenerateJob(t *testing.T) {
	p, meta := genPool(t)
	ctx := context.Background()
	m := mockllm.New(mockllm.Options{})
	m.Script(mockllm.Content, mockllm.Stall(`{"sms":{"body":"Mer`))
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	cl, err := llm.New(llm.Config{BaseURL: srv.URL, APIKey: "test-placeholder", Schemas: schemas(t),
		StallTimeout: time.Minute, TotalTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	g := &Generator{Pool: p, Schemas: schemas(t), LLM: cl, Now: func() time.Time { return meta.AsOf },
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }}
	workers := river.NewWorkers()
	river.AddWorker(workers, NewGenerateWorker(g))
	rc, err := river.NewClient(riverpgxv5.New(p), jobs.ClientConfig(workers, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = rc.Stop(stop)
	})

	id := insertGenerating(t, p, exampleBrief, 0)
	if _, err := p.Exec(ctx, `UPDATE campaigns SET state = 'draft' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := StartGeneration(ctx, p, rc, id, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for len(recorded(t, m, mockllm.Content)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the content stage never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := CancelWithJobs(ctx, p, rc, id, 1, "admin"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for {
		got := jobState(t, p, id)
		if len(got) == 1 && (got[0] == "cancelled" || got[0] == "completed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job state %v 8s after the cancel; the running job was not interrupted", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(recorded(t, m, mockllm.Content)); n != 1 {
		t.Errorf("%d content requests after the cancel, want 1", n)
	}
	if got := loadGen(t, p, id); got.State != Cancelled || got.Revisions != 0 {
		t.Errorf("campaign %+v, want cancelled with no revision", got)
	}
	events := genEvents(t, p, id)
	if last := events[len(events)-1]; last.Type != "failed" || last.Payload["reason"] != ReasonCancelled {
		t.Errorf("last event %+v; nothing may follow the cancel", last)
	}
}
