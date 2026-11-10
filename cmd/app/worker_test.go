package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

var quiet = slog.New(slog.DiscardHandler)

// Süreç veritabanına dokunmadan kapanır.
func TestWorkerFailsClosed(t *testing.T) {
	ctx := context.Background()
	if err := workerCmd(ctx, quiet, env(map[string]string{"LLM_MODE": "anthropic", "DATABASE_URL": "postgres://unused"})); !errors.Is(err, llm.ErrNoAPIKey) {
		t.Errorf("anthropic mode without a key: %v, want ErrNoAPIKey", err)
	}
	if err := workerCmd(ctx, quiet, env(map[string]string{"DATABASE_URL": "postgres://unused"})); !errors.Is(err, llm.ErrUnknownMode) {
		t.Errorf("no LLM_MODE: %v, want ErrUnknownMode", err)
	}
	if err := workerCmd(ctx, quiet, env(map[string]string{"LLM_MODE": "mock", "PROVIDER_URL": "http://unused"})); err == nil {
		t.Error("no DATABASE_URL: started anyway")
	}
	if err := workerCmd(ctx, quiet, env(map[string]string{"LLM_MODE": "mock", "DATABASE_URL": "postgres://unused"})); err == nil {
		t.Error("no PROVIDER_URL: started anyway")
	}
}

// İlk yanıtı bozan bayrak onarım döngüsünü gösterir.
func TestMockllmInvalidFirst(t *testing.T) {
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, invalidFirst := range []bool{false, true} {
		v := map[bool]string{true: "true"}[invalidFirst]
		srv := httptest.NewServer(mockllmHandler(env(map[string]string{"MOCKLLM_INVALID_FIRST": v})))
		cl, err := llm.New(llm.Config{BaseURL: srv.URL, APIKey: llm.MockAPIKey, Schemas: set})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := cl.Call(context.Background(), llm.NewConversation(llm.Segment, llm.SegmentTurn("İstanbul sepet")), nil)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		_, res := segment.ValidateJSON(set, []byte(resp.Text))
		if res.OK() == invalidFirst {
			t.Errorf("MOCKLLM_INVALID_FIRST=%q: first answer valid=%v", v, res.OK())
		}
	}
}

// Sahte kipte üretim uçtan uca yürür ve bağlam bitince durur.
func TestWorkerRunsGeneration(t *testing.T) {
	url, _ := testdb.Seeded(t)
	srv := httptest.NewServer(mockllmHandler(env(nil)))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- workerCmd(ctx, quiet, env(map[string]string{"LLM_MODE": "mock", "MOCKLLM_URL": srv.URL, "DATABASE_URL": url,
			"PROVIDER_URL": "http://127.0.0.1:9"}))
	}()

	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rc, err := river.NewClient(riverpgxv5.New(p), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := campaign.Create(ctx, p, campaign.New{Brief: "Son 30 günde sepete ekleyip almayan İstanbul kullanıcıları", CampaignURL: "https://shop.example/k"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := campaign.StartGeneration(ctx, p, rc, c.ID, c.Version, "admin"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		var state string
		if err := p.QueryRow(ctx, `SELECT state::text FROM campaigns WHERE id = $1`, c.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "in_review" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state %s after 30s, want in_review", state)
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("worker stopped with %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Error("worker did not stop")
	}
}

// Onaylı kampanya dağılır ve her ileti sağlayıcıya bir kez gider.
func TestWorkerDeliversCampaign(t *testing.T) {
	url, _ := testdb.Seeded(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prov, cleanup, err := newMockprovider(ctx, env(map[string]string{"MOCKPROVIDER_DATABASE_URL": testdb.Fresh(t)}))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	provSrv := httptest.NewServer(prov)
	defer provSrv.Close()
	done := make(chan error, 1)
	go func() {
		done <- workerCmd(ctx, quiet, env(map[string]string{"LLM_MODE": "mock", "DATABASE_URL": url, "PROVIDER_URL": provSrv.URL}))
	}()

	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rc, err := river.NewClient(riverpgxv5.New(p), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var id, rev string
	if err := p.QueryRow(ctx, `INSERT INTO campaigns (brief, campaign_url) VALUES ('b', 'https://shop.example/k') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// Ankara altın üyeleri az olduğu için koşu kısa sürer.
	if err := p.QueryRow(ctx, `INSERT INTO campaign_revisions (campaign_id, n, source, segment, sms_body, email_subject, email_body,
			audience_total, sms_eligible, email_eligible)
		VALUES ($1, 1, 'llm', '{"version":1,"match":"all","conditions":[{"kind":"enum_attribute","field":"city","op":"eq","value":"ankara"},
			{"kind":"enum_attribute","field":"loyalty_tier","op":"eq","value":"gold"}]}',
			'Merhaba {{first_name}} {{campaign_url}}', 'Selam', 'Merhaba {{first_name}}', 1, 1, 1) RETURNING id::text`, id).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE campaigns SET state = 'in_review', current_revision_id = $2 WHERE id = $1`, id, rev); err != nil {
		t.Fatal(err)
	}
	if err := campaign.Approve(ctx, p, rc, id, rev, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		var state string
		if err := p.QueryRow(ctx, `SELECT state::text FROM campaigns WHERE id = $1`, id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("campaign %s after 60s, want completed", state)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var sent, total int
	if err := p.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state = 'sent'), count(*) FROM messages WHERE campaign_id = $1`, id).Scan(&sent, &total); err != nil {
		t.Fatal(err)
	}
	if total == 0 || sent != total {
		t.Errorf("%d of %d messages sent", sent, total)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("worker stopped with %v", err)
	}
}
