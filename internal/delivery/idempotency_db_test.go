package delivery

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockprovider"
)

func TestSendThenDrop(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.prov.force(id, "drop", "ok")

	if _, ok := snoozed(e.work(id)); !ok {
		t.Fatal("drop was not retried")
	}
	if err := e.work(id); err != nil {
		t.Fatalf("retry: %v", err)
	}
	assertOneSend(t, e, id)
}

func TestSendThenLateAnswer(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.prov.force(id, "late", "ok")

	if _, ok := snoozed(e.work(id)); !ok {
		t.Fatal("late answer was not retried")
	}
	if err := e.work(id); err != nil {
		t.Fatalf("retry: %v", err)
	}
	assertOneSend(t, e, id)
}

func assertOneSend(t *testing.T, e *sendEnv, id string) {
	t.Helper()
	m := loadMessage(t, e.pool, id)
	if m.State != "sent" || str(m.ProviderID) == "" {
		t.Errorf("message %+v; want sent with a provider id", m)
	}
	s := e.prov.stats(t)
	if s.UniqueSends != 1 || s.MaxSendsPerKey != 1 || s.Replays < 1 {
		t.Errorf("provider stats %+v; want 1 send and the retry as a replay", s)
	}
	calls := e.prov.callsFor(id)
	if len(calls) < 2 || len(calls) != len(e.prov.all()) {
		t.Errorf("%d requests carried the message id as key, %d in all; want every retry to carry it", len(calls), len(e.prov.all()))
	}
	if e.tokensTaken() != int64(len(e.prov.all())) {
		t.Errorf("%d tokens for %d provider calls", e.tokensTaken(), len(e.prov.all()))
	}
}

func TestCrashAfterProviderCall(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	db := &flakyDB{Pool: e.pool}
	e.worker.DB = db
	e.prov.hooks(nil, func(string) {
		if !db.down.Load() {
			db.down.Store(true)
		}
	})

	err := e.work(id)
	if err == nil {
		t.Fatal("the crashed run reported success")
	}
	if _, ok := snoozed(err); ok || cancelled(err) {
		t.Fatalf("the crashed run returned %v; want a plain error so River retries", err)
	}
	if m := loadMessage(t, e.pool, id); m.State != "sending" || m.Attempts != 1 {
		t.Fatalf("after the crash: %+v; want sending with 1 attempt", m)
	}
	if s := e.prov.stats(t); s.UniqueSends != 1 {
		t.Fatalf("the provider did not record the first send: %+v", s)
	}
	if calls := e.prov.callsFor(id); len(calls) != 1 || calls[0].Status != 202 {
		t.Fatalf("first call %+v; want one call answered 202", calls)
	}

	e.prov.hooks(nil, nil)
	db.down.Store(false)
	if err := e.work(id); err != nil {
		t.Fatalf("River's retry: %v", err)
	}
	m := loadMessage(t, e.pool, id)
	if m.State != "sent" || m.Attempts != 2 {
		t.Errorf("after the retry: %+v; want sent, attempts 2 (one too high is the known cost)", m)
	}
	s := e.prov.stats(t)
	if s.UniqueSends != 1 || s.Replays != 1 || s.MaxSendsPerKey != 1 {
		t.Errorf("provider stats %+v; want 1 send and 1 replay", s)
	}
}

func TestReclaimStuckSending(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms",
		"state = 'sending', attempts = 1, first_attempt_at = now()")
	if err := e.work(id); err != nil {
		t.Fatalf("work: %v", err)
	}
	m := loadMessage(t, e.pool, id)
	if m.State != "sent" || m.Attempts != 2 {
		t.Errorf("message %+v; want sent after 2 attempts", m)
	}
	if s := e.prov.stats(t); s.UniqueSends != 1 || s.MaxSendsPerKey != 1 {
		t.Errorf("provider stats %+v", s)
	}
}

func TestSameJobTwiceAtOnce(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")

	var arrived sync.WaitGroup
	arrived.Add(2)
	both := make(chan struct{})
	go func() { arrived.Wait(); close(both) }()
	e.prov.hooks(func(string) {
		arrived.Done()
		select {
		case <-both:
		case <-time.After(2 * time.Second):
		}
	}, nil)

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() { errs[i] = e.work(id) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil && !cancelled(err) {
			t.Errorf("run %d: %v", i, err)
		}
	}
	m := loadMessage(t, e.pool, id)
	if m.State != "sent" || str(m.ProviderID) == "" {
		t.Errorf("message %+v; want sent", m)
	}
	s := e.prov.stats(t)
	if s.UniqueSends != 1 || s.MaxSendsPerKey != 1 {
		t.Errorf("provider stats %+v; want exactly 1 send", s)
	}
	if n := len(e.prov.callsFor(id)); n == 2 && s.Replays != 1 {
		t.Errorf("2 requests but %d replays", s.Replays)
	}
	if got := auditTrail(t, e.pool, c.ID); !slices.Equal(got, []string{"sending>completed"}) {
		t.Errorf("audit trail %v; want the campaign completed once", got)
	}
}

func TestEndToEndChaos(t *testing.T) {
	if testing.Short() {
		t.Skip("end to end run")
	}
	pool, meta := appPool(t)
	prov := newProvider(t, mockprovider.Config{
		Seed:       42,
		Latency:    5 * time.Millisecond,
		Rate500:    0.05,
		Rate503:    0.05,
		Rate429:    0.05,
		RateDrop:   0.03,
		RateHang:   0.02,
		RetryAfter: time.Second,
	})
	c := insertCampaignIn(t, pool, "approved", 2)

	send := &SendWorker{
		DB:              pool,
		ProviderURL:     prov.URL,
		Limiters:        NewLimiters(),
		ProviderTimeout: 300 * time.Millisecond,
		Now:             time.Now,
		Backoff:         func(int) time.Duration { return 50 * time.Millisecond },
		Log:             slog.New(slog.DiscardHandler),
	}
	fanout := &FanoutWorker{DB: pool, Now: func() time.Time { return meta.AsOf }}
	workers := river.NewWorkers()
	river.AddWorker(workers, fanout)
	river.AddWorker(workers, send)
	cfg := jobs.ClientConfig(workers, slog.New(slog.DiscardHandler))
	cfg.FetchCooldown = 10 * time.Millisecond
	cfg.FetchPollInterval = 50 * time.Millisecond
	cfg.TestOnly = true
	client, err := river.NewClient(riverpgxv5.New(pool), cfg)
	if err != nil {
		t.Fatal(err)
	}
	fanout.Jobs = client

	ctx := context.Background()
	if err := fanout.Work(ctx, fanoutJob(c.ID)); err != nil {
		t.Fatal(err)
	}
	msgs := messagesOf(t, pool, c.ID)
	if len(msgs) < 400 {
		t.Fatalf("only %d messages; want about 300 users on 2 channels", len(msgs))
	}
	forced := map[string]bool{}
	for _, m := range msgs[:3] {
		prov.force(m.ID, "400")
		forced[m.ID] = true
	}

	runCtx, stop := context.WithCancel(ctx)
	if err := client.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
		stop()
	})

	deadline := time.Now().Add(2 * time.Minute)
	for {
		state, _ := campaignState(t, pool, c.ID)
		if state == "completed" || state == "completed_with_failures" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("campaign still %s after 2 minutes", state)
		}
		time.Sleep(200 * time.Millisecond)
	}

	counts := map[string]int{}
	rows, err := pool.Query(ctx, `SELECT state::text, count(*) FROM messages WHERE campaign_id = $1 GROUP BY 1`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			t.Fatal(err)
		}
		counts[s] = n
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if counts["queued"] != 0 || counts["sending"] != 0 {
		t.Errorf("messages left unfinished: %v", counts)
	}

	s := prov.stats(t)
	if s.MaxSendsPerKey != 1 {
		t.Errorf("max sends per key %d, want 1", s.MaxSendsPerKey)
	}
	ambiguousDead := 0
	for _, m := range msgs {
		if loadMessage(t, pool, m.ID).State == "dead" &&
			slices.ContainsFunc(prov.callsFor(m.ID), func(cl call) bool { return cl.Status == 0 }) {
			ambiguousDead++
		}
	}
	if s.UniqueSends < counts["sent"] || s.UniqueSends > counts["sent"]+ambiguousDead {
		t.Errorf("%d messages sent and %d dead after an ambiguous call; the provider sent %d", counts["sent"], ambiguousDead, s.UniqueSends)
	}

	for _, m := range msgs {
		calls := prov.callsFor(m.ID)
		for i, cl := range calls {
			if cl.Status >= 400 && cl.Status < 500 && cl.Status != 408 && cl.Status != 429 && i != len(calls)-1 {
				t.Errorf("message %s was retried after a %d", m.ID, cl.Status)
			}
		}
		if forced[m.ID] {
			if row := loadMessage(t, pool, m.ID); row.State != "failed" || row.Attempts != 1 || len(calls) != 1 {
				t.Errorf("forced 400 on %s: %+v after %d calls; want failed after 1", m.ID, row, len(calls))
			}
		}
	}
	if state, _ := campaignState(t, pool, c.ID); state != "completed_with_failures" {
		t.Errorf("campaign %s; three messages failed, want completed_with_failures", state)
	}

	for ch, times := range s.ReceivedAt {
		if w := maxInWindow(times, time.Second); w > 105 {
			t.Errorf("%s: %d requests arrived within one second", ch, w)
		}
	}
	if total := len(prov.all()); total <= len(msgs) {
		t.Errorf("%d provider calls for %d messages; the chaos did not cause retries", total, len(msgs))
	}
	if !slices.ContainsFunc(prov.all(), func(cl call) bool { return cl.Status == 0 }) {
		t.Error("no dropped or hung call; the chaos rates did not apply")
	}
}
