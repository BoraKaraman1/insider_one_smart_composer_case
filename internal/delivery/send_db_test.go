package delivery

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockprovider"
)

func TestSendWorkerTimeout(t *testing.T) {
	if got := (&SendWorker{}).Timeout(sendJob("x")); got != jobs.SendTimeout {
		t.Errorf("job timeout %v, want %v", got, jobs.SendTimeout)
	}
	if ProviderTimeout != 5*time.Second {
		t.Errorf("provider timeout %v, want 5s", ProviderTimeout)
	}
	if MaxAttempts != 6 || MaxAge != 15*time.Minute {
		t.Errorf("dead after %d attempts or %v; want 6 and 15m", MaxAttempts, MaxAge)
	}
}

func TestSendDeliversRenderedMessage(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	user := consentingUsers(t, e.pool, 1)[0]
	smsID := insertMessage(t, e.pool, c, user, "sms", "")
	emailID := insertMessage(t, e.pool, c, user, "email", "")

	for _, id := range []string{smsID, emailID} {
		if err := e.work(id); err != nil {
			t.Fatalf("work %s: %v", id, err)
		}
	}

	var first, last, city, phone, email string
	if err := e.pool.QueryRow(context.Background(), `SELECT first_name, last_name, city, phone, email FROM users WHERE id = $1`, user).
		Scan(&first, &last, &city, &phone, &email); err != nil {
		t.Fatal(err)
	}
	rc := content.RenderContext{FirstName: first, LastName: last, City: city, CampaignURL: campaignURL}
	wantSMS, err := content.RenderText(smsBody, rc)
	if err != nil {
		t.Fatal(err)
	}
	wantSubject, err := content.RenderText(emailSubject, rc)
	if err != nil {
		t.Fatal(err)
	}

	sms := e.prov.callsFor(smsID)
	if len(sms) != 1 {
		t.Fatalf("sms: %d requests, want 1", len(sms))
	}
	if b := sms[0].Body; b["channel"] != "sms" || b["to"] != phone || b["body"] != wantSMS {
		t.Errorf("sms payload %v; want channel sms, to %q, body %q", b, phone, wantSMS)
	}
	if _, has := sms[0].Body["subject"]; has {
		t.Errorf("sms payload has a subject: %v", sms[0].Body)
	}
	mail := e.prov.callsFor(emailID)
	if len(mail) != 1 {
		t.Fatalf("email: %d requests, want 1", len(mail))
	}
	if b := mail[0].Body; b["channel"] != "email" || b["to"] != email || b["subject"] != wantSubject || b["body"] == "" || b["body"] == nil {
		t.Errorf("email payload %v; want channel email, to %q, subject %q and a body", b, email, wantSubject)
	}

	for _, id := range []string{smsID, emailID} {
		m := loadMessage(t, e.pool, id)
		if m.State != "sent" || m.Attempts != 1 || str(m.ProviderID) == "" || m.SentAt == nil || m.FirstAttemptAt == nil {
			t.Errorf("message %s: %+v; want sent after 1 attempt with a provider id", id, m)
		}
		if m.FirstAttemptAt != nil && !m.FirstAttemptAt.Equal(e.now) {
			t.Errorf("first_attempt_at %v, want the worker's now %v", m.FirstAttemptAt, e.now)
		}
	}
	if got := e.tokensTaken(); got != 2 {
		t.Errorf("%d limiter tokens for 2 provider calls", got)
	}
	if e.tokens["sms"].n.Load() != 1 || e.tokens["email"].n.Load() != 1 {
		t.Error("a message took its token from the other channel's limiter")
	}
	for _, secret := range []string{phone, email, wantSMS, wantSubject} {
		if strings.Contains(e.logs.String(), secret) {
			t.Errorf("logs contain %q", secret)
		}
	}
}

func TestServerErrorThenOK(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.prov.force(id, "503", "ok")

	err := e.work(id)
	d, ok := snoozed(err)
	if !ok || d != fixedBackoff {
		t.Fatalf("after 503: %v; want JobSnooze(%v)", err, fixedBackoff)
	}
	m := loadMessage(t, e.pool, id)
	if m.State != "queued" || m.Attempts != 1 || str(m.LastErrorClass) != "server_error" ||
		m.NextAttemptAt == nil || !m.NextAttemptAt.Equal(e.now.Add(fixedBackoff)) {
		t.Fatalf("after 503: %+v; want queued, 1 attempt, server_error, next attempt at now+%v", m, fixedBackoff)
	}
	if !slices.Equal(e.retries, []int{0}) {
		t.Errorf("Backoff called with %v; want [0] (0 is the first retry)", e.retries)
	}

	if err := e.work(id); err != nil {
		t.Fatalf("second run: %v", err)
	}
	m = loadMessage(t, e.pool, id)
	if m.State != "sent" || m.Attempts != 2 {
		t.Errorf("after 503 then ok: %+v; want sent after 2 attempts", m)
	}
	s := e.prov.stats(t)
	if s.UniqueSends != 1 || s.MaxSendsPerKey != 1 || len(e.prov.callsFor(id)) != 2 {
		t.Errorf("provider stats %+v; want 2 requests and 1 send", s)
	}
	if e.tokensTaken() != 2 {
		t.Errorf("%d tokens for 2 provider calls", e.tokensTaken())
	}
}

func TestRateLimitedHonorsRetryAfter(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{RetryAfter: 2 * time.Second})
	c := insertSendingCampaign(t, e.pool)
	users := consentingUsers(t, e.pool, 2)
	with := insertMessage(t, e.pool, c, users[0], "sms", "")
	without := insertMessage(t, e.pool, c, users[1], "sms", "")
	e.prov.force(with, "429")
	e.prov.force(without, "429-no-retry-after")

	cases := []struct {
		id   string
		want time.Duration
	}{
		{with, 2 * time.Second},
		{without, fixedBackoff},
	}
	for _, cs := range cases {
		err := e.work(cs.id)
		if d, ok := snoozed(err); !ok || d != cs.want {
			t.Errorf("429: %v; want JobSnooze(%v)", err, cs.want)
		}
		m := loadMessage(t, e.pool, cs.id)
		if m.State != "queued" || str(m.LastErrorClass) != "rate_limited" || m.NextAttemptAt == nil || !m.NextAttemptAt.Equal(e.now.Add(cs.want)) {
			t.Errorf("429: %+v; want queued, rate_limited, next attempt at now+%v", m, cs.want)
		}
	}
}

func TestClientErrorFailsWithoutRetry(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.prov.force(id, "400")

	err := e.work(id)
	if !cancelled(err) {
		t.Fatalf("after 400: %v; want river.JobCancel", err)
	}
	m := loadMessage(t, e.pool, id)
	if m.State != "failed" || str(m.LastErrorClass) != "client_error" || m.Attempts != 1 {
		t.Errorf("after 400: %+v; want failed, client_error, 1 attempt", m)
	}
	if n := len(e.prov.callsFor(id)); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
	if len(e.retries) != 0 {
		t.Errorf("Backoff was consulted for a permanent error: %v", e.retries)
	}
}

func TestAlwaysFailingIsDeadAfterSixAttempts(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.prov.force(id, slices.Repeat([]string{"503"}, 10)...)

	var err error
	runs := 0
	for runs < 10 {
		runs++
		if err = e.work(id); err == nil || cancelled(err) {
			break
		}
		if _, ok := snoozed(err); !ok {
			t.Fatalf("run %d: %v", runs, err)
		}
	}
	m := loadMessage(t, e.pool, id)
	if m.State != "dead" || m.Attempts != MaxAttempts || str(m.LastErrorClass) != "server_error" {
		t.Errorf("after %d runs: %+v; want dead after %d attempts, server_error", runs, m, MaxAttempts)
	}
	if n := len(e.prov.callsFor(id)); n != MaxAttempts {
		t.Errorf("%d requests, want %d", n, MaxAttempts)
	}
	if e.tokensTaken() != MaxAttempts {
		t.Errorf("%d tokens for %d provider calls", e.tokensTaken(), MaxAttempts)
	}
	if _, ok := snoozed(err); ok {
		t.Errorf("the last run snoozed: %v", err)
	}
	if err := e.work(id); err != nil && !cancelled(err) {
		t.Errorf("a run on a dead message: %v", err)
	}
	if n := len(e.prov.callsFor(id)); n != MaxAttempts {
		t.Errorf("a dead message was sent again: %d requests", n)
	}
}

func TestDeadAfterMaxAge(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	users := consentingUsers(t, e.pool, 2)
	old := insertMessage(t, e.pool, c, users[0], "sms", "")
	young := insertMessage(t, e.pool, c, users[1], "sms", "")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE messages SET attempts = 1, last_error_class = 'server_error', first_attempt_at = $2 WHERE id = $1`,
		old, e.now.Add(-16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE messages SET attempts = 1, last_error_class = 'server_error', first_attempt_at = $2 WHERE id = $1`,
		young, e.now.Add(-14*time.Minute)); err != nil {
		t.Fatal(err)
	}
	e.prov.force(young, "503")

	if err := e.work(old); err != nil {
		t.Errorf("old message: %v; want no retry", err)
	}
	if m := loadMessage(t, e.pool, old); m.State != "dead" || m.Attempts != 1 || str(m.LastErrorClass) != "server_error" {
		t.Errorf("old message: %+v; want dead with no new attempt", m)
	}
	if n := len(e.prov.callsFor(old)); n != 0 {
		t.Errorf("%d provider calls for a message past its age limit, want 0 even though the provider would accept it", n)
	}
	if _, ok := snoozed(e.work(young)); !ok {
		t.Error("a message 14 minutes into its retries was not retried")
	}
	if m := loadMessage(t, e.pool, young); m.State != "queued" {
		t.Errorf("young message: %+v; want queued", m)
	}
	if m := loadMessage(t, e.pool, young); m.FirstAttemptAt == nil || !m.FirstAttemptAt.Equal(e.now.Add(-14*time.Minute)) {
		t.Errorf("first_attempt_at moved: %+v", m)
	}
}

func TestConsentRevokedBeforeSending(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	user := consentingUsers(t, e.pool, 1)[0]
	sms := insertMessage(t, e.pool, c, user, "sms", "")
	email := insertMessage(t, e.pool, c, user, "email", "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET consent_sms = false WHERE id = $1`, user); err != nil {
		t.Fatal(err)
	}

	if err := e.work(sms); err != nil {
		t.Fatalf("sms: %v", err)
	}
	if m := loadMessage(t, e.pool, sms); m.State != "skipped" || str(m.LastErrorClass) != "no_consent" || m.Attempts != 0 {
		t.Errorf("sms: %+v; want skipped, no_consent, 0 attempts", m)
	}
	if n := len(e.prov.callsFor(sms)); n != 0 || e.tokens["sms"].n.Load() != 0 {
		t.Errorf("sms without consent: %d requests, %d tokens; want none", n, e.tokens["sms"].n.Load())
	}
	if err := e.work(email); err != nil {
		t.Fatalf("email: %v", err)
	}
	if m := loadMessage(t, e.pool, email); m.State != "sent" {
		t.Errorf("email: %+v; want sent", m)
	}
}

func TestCancelledCampaignSkips(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE campaigns SET state = 'cancelled' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.work(id); err != nil {
		t.Fatalf("work: %v", err)
	}
	if m := loadMessage(t, e.pool, id); m.State != "skipped" || str(m.LastErrorClass) != "cancelled" {
		t.Errorf("message %+v; want skipped, cancelled", m)
	}
	if n := len(e.prov.all()); n != 0 || e.tokensTaken() != 0 {
		t.Errorf("%d requests and %d tokens for a cancelled campaign", n, e.tokensTaken())
	}
}

func TestTerminalMessagesAreLeftAlone(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	users := consentingUsers(t, e.pool, 4)
	extras := map[string]string{
		"sent":    `state = 'sent', attempts = 1, provider_message_id = 'p-1', sent_at = now()`,
		"failed":  `state = 'failed', attempts = 1, last_error_class = 'client_error'`,
		"dead":    `state = 'dead', attempts = 6, last_error_class = 'server_error'`,
		"skipped": `state = 'skipped', last_error_class = 'cancelled'`,
	}
	i := 0
	for state, extra := range extras {
		id := insertMessage(t, e.pool, c, users[i], "sms", extra)
		i++
		if err := e.work(id); err != nil && !cancelled(err) {
			t.Errorf("%s: %v", state, err)
		}
		if m := loadMessage(t, e.pool, id); m.State != state {
			t.Errorf("%s message moved to %s", state, m.State)
		}
	}
	if n := len(e.prov.all()); n != 0 || e.tokensTaken() != 0 {
		t.Errorf("%d requests and %d tokens for terminal messages", n, e.tokensTaken())
	}
}

func TestSentUpdateIsGuarded(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.prov.hooks(func(string) {
		if _, err := e.pool.Exec(context.Background(), `UPDATE messages SET state = 'sent', provider_message_id = 'other-run', sent_at = now()
			WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	}, nil)
	if err := e.work(id); err != nil && !cancelled(err) {
		t.Fatalf("work: %v", err)
	}
	if m := loadMessage(t, e.pool, id); m.State != "sent" || str(m.ProviderID) != "other-run" {
		t.Errorf("message %+v; the guarded update overwrote another run's result", m)
	}
}

func TestRetryableReasons(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	users := consentingUsers(t, e.pool, 4)
	cases := []struct{ chaos, class string }{
		{"500", "server_error"},
		{"drop", "conn_reset"},
		{"hang", "timeout"},
		{"late", "timeout"},
	}
	for i, cs := range cases {
		id := insertMessage(t, e.pool, c, users[i], "sms", "")
		e.prov.force(id, cs.chaos)
		if _, ok := snoozed(e.work(id)); !ok {
			t.Errorf("%s: not snoozed", cs.chaos)
		}
		if m := loadMessage(t, e.pool, id); m.State != "queued" || str(m.LastErrorClass) != cs.class {
			t.Errorf("%s: %+v; want queued with %s", cs.chaos, m, cs.class)
		}
	}
}

func TestProviderDown(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	e.prov.srv.Close()
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	if _, ok := snoozed(e.work(id)); !ok {
		t.Fatal("not snoozed")
	}
	if m := loadMessage(t, e.pool, id); m.State != "queued" || str(m.LastErrorClass) != "conn_refused" || m.Attempts != 1 {
		t.Errorf("message %+v; want queued, conn_refused, 1 attempt", m)
	}
	if e.tokensTaken() != 1 {
		t.Errorf("%d tokens for one attempt", e.tokensTaken())
	}
}

func TestCampaignCompletion(t *testing.T) {
	cases := []struct {
		name   string
		second string
		want   string
	}{
		{"all sent", "ok", "completed"},
		{"one failed", "400", "completed_with_failures"},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			e := newSendEnv(t, mockprovider.Config{})
			c := insertSendingCampaign(t, e.pool)
			users := consentingUsers(t, e.pool, 2)
			a := insertMessage(t, e.pool, c, users[0], "sms", "")
			b := insertMessage(t, e.pool, c, users[1], "email", "")
			e.prov.force(b, cs.second)

			if err := e.work(a); err != nil {
				t.Fatal(err)
			}
			if state, _ := campaignState(t, e.pool, c.ID); state != "sending" {
				t.Fatalf("one message left: campaign %s, want sending", state)
			}
			if err := e.work(b); err != nil && !cancelled(err) {
				t.Fatal(err)
			}
			state, version := campaignState(t, e.pool, c.ID)
			if state != cs.want || version != 4 {
				t.Errorf("campaign %s version %d; want %s version 4", state, version, cs.want)
			}
			if err := e.work(a); err != nil {
				t.Fatal(err)
			}
			want := []string{"sending>" + cs.want}
			if got := auditTrail(t, e.pool, c.ID); !slices.Equal(got, want) {
				t.Errorf("audit trail %v, want %v", got, want)
			}
		})
	}
}

func TestCompletionCountsSkippedAsDone(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	user := consentingUsers(t, e.pool, 1)[0]
	id := insertMessage(t, e.pool, c, user, "sms", "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET consent_sms = false WHERE id = $1`, user); err != nil {
		t.Fatal(err)
	}
	if err := e.work(id); err != nil {
		t.Fatal(err)
	}
	if state, _ := campaignState(t, e.pool, c.ID); state != "completed" {
		t.Errorf("campaign %s, want completed", state)
	}
}

func TestUnknownMessage(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	err := e.work("00000000-0000-4000-8000-00000000dead")
	if err != nil && !cancelled(err) {
		t.Errorf("unknown message: %v; want nil or JobCancel", err)
	}
	if errors.Is(err, errCrash) || len(e.prov.all()) != 0 {
		t.Error("an unknown message reached the provider")
	}
}

type hookLimiter struct{ during func() }

func (h hookLimiter) Wait(ctx context.Context) error {
	h.during()
	return ctx.Err()
}

// İlk sahiplenme sorgusundan hemen önce bir kez çalışır.
type beforeClaimDB struct {
	*pgxpool.Pool
	once sync.Once
	hook func()
}

func isClaim(sql string) bool { return strings.Contains(sql, "SET state = 'sending'") }

func (d *beforeClaimDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if isClaim(sql) {
		d.once.Do(d.hook)
	}
	return d.Pool.QueryRow(ctx, sql, args...)
}

func TestConsentRevokedBeforeClaim(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	user := consentingUsers(t, e.pool, 1)[0]
	id := insertMessage(t, e.pool, c, user, "sms", "")
	e.worker.DB = &beforeClaimDB{Pool: e.pool, hook: func() {
		if _, err := e.pool.Exec(context.Background(), `UPDATE users SET consent_sms = false WHERE id = $1`, user); err != nil {
			t.Error(err)
		}
	}}

	if err := e.work(id); err != nil {
		t.Fatalf("work: %v", err)
	}
	if n := len(e.prov.callsFor(id)); n != 0 {
		t.Fatalf("%d provider calls after consent was revoked", n)
	}
	if e.tokensTaken() != 0 {
		t.Errorf("%d tokens taken for a message that was never sent", e.tokensTaken())
	}
	if m := loadMessage(t, e.pool, id); m.State != "skipped" || str(m.LastErrorClass) != "no_consent" || m.Attempts != 0 {
		t.Errorf("message %+v; want skipped, no_consent, 0 attempts", m)
	}
	if state, _ := campaignState(t, e.pool, c.ID); state != "completed" {
		t.Errorf("campaign %s, want completed", state)
	}
}

func TestCancelledBeforeClaim(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.worker.DB = &beforeClaimDB{Pool: e.pool, hook: func() {
		if _, err := e.pool.Exec(context.Background(), `UPDATE campaigns SET state = 'cancelled' WHERE id = $1`, c.ID); err != nil {
			t.Error(err)
		}
	}}

	if err := e.work(id); err != nil {
		t.Fatalf("work: %v", err)
	}
	if n := len(e.prov.all()); n != 0 {
		t.Fatalf("%d provider calls for a cancelled campaign", n)
	}
	if m := loadMessage(t, e.pool, id); m.State != "skipped" || str(m.LastErrorClass) != "cancelled" {
		t.Errorf("message %+v; want skipped, cancelled", m)
	}
}

func TestTokenIsTakenAfterTheClaim(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	waits := 0
	e.worker.Limiters["sms"] = hookLimiter{during: func() {
		waits++
		if m := loadMessage(t, e.pool, id); m.State != "sending" || m.Attempts != 1 {
			t.Errorf("at the limiter the message is %+v; want it already claimed, so no database work sits between the token and the call", m)
		}
		if n := len(e.prov.callsFor(id)); n != 0 {
			t.Errorf("%d provider calls before the token", n)
		}
	}}
	if err := e.work(id); err != nil {
		t.Fatalf("work: %v", err)
	}
	if waits != 1 || len(e.prov.callsFor(id)) != 1 {
		t.Errorf("%d limiter waits and %d provider calls, want 1 and 1", waits, len(e.prov.callsFor(id)))
	}
}

// Sahiplenme sorguları kapı açılana kadar bekler, havuz sıkışması gibi.
type stallClaimDB struct {
	*pgxpool.Pool
	gate chan struct{}
}

func (d *stallClaimDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if isClaim(sql) {
		<-d.gate
	}
	return d.Pool.QueryRow(ctx, sql, args...)
}

type dispatchRecorder struct {
	mu    sync.Mutex
	times []time.Time
}

func (r *dispatchRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.times = append(r.times, time.Now())
	r.mu.Unlock()
	return http.DefaultTransport.RoundTrip(req)
}

func TestStalledClaimsDoNotBurstTheProvider(t *testing.T) {
	const messages = 130
	workers := jobs.QueueWorkers[jobs.QueueSMS]
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	ids := make(chan string, messages)
	for _, u := range consentingUsers(t, e.pool, messages) {
		ids <- insertMessage(t, e.pool, c, u, "sms", "")
	}
	close(ids)
	db := &stallClaimDB{Pool: e.pool, gate: make(chan struct{})}
	rec := &dispatchRecorder{}
	e.worker.DB = db
	e.worker.Limiters = map[string]Limiter{"sms": NewLimiter()}
	e.worker.ProviderTimeout = ProviderTimeout
	e.worker.Client = &http.Client{Transport: rec}

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for id := range ids {
				if err := e.work(id); err != nil {
					t.Errorf("work: %v", err)
				}
			}
		})
	}
	// Eski sırada her işçi bu sürede bir jeton alır ve kapı açılınca hepsi birden çağırırdı.
	time.Sleep(time.Duration(workers) * time.Second / RatePerSecond * 2)
	close(db.gate)
	wg.Wait()

	if len(rec.times) != messages {
		t.Fatalf("%d provider calls, want %d", len(rec.times), messages)
	}
	worst := 0
	for i, start := range rec.times {
		n := 0
		for _, at := range rec.times[i:] {
			if at.Sub(start) < time.Second {
				n++
			}
		}
		worst = max(worst, n)
	}
	// Sınırlayıcı ilk jetonu beklemeden verir, bu yüzden yarı açık pencerede en çok yüz bir çağrı olur.
	if worst > RatePerSecond+1 {
		t.Errorf("%d provider calls inside one second, want at most %d", worst, RatePerSecond)
	}
}

func TestExhaustedMessagesAreNotSentAgain(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	users := consentingUsers(t, e.pool, 2)
	// Biri çöken denemeden kalmış, öteki yeniden deneme sırasında.
	reclaimed := insertMessage(t, e.pool, c, users[0], "sms", "")
	queued := insertMessage(t, e.pool, c, users[1], "sms", "")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE messages SET state = 'sending', attempts = $2, first_attempt_at = $3 WHERE id = $1`,
		reclaimed, MaxAttempts, e.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE messages SET attempts = $2, last_error_class = 'rate_limited', last_error = 'provider answered 429',
		first_attempt_at = $3 WHERE id = $1`, queued, MaxAttempts, e.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{reclaimed, queued} {
		if err := e.work(id); err != nil {
			t.Fatalf("work: %v", err)
		}
		if m := loadMessage(t, e.pool, id); m.State != "dead" || m.Attempts != MaxAttempts {
			t.Errorf("message %+v; want dead with %d attempts", m, MaxAttempts)
		}
	}
	if n := len(e.prov.all()); n != 0 {
		t.Errorf("%d provider calls for messages that had used all %d attempts", n, MaxAttempts)
	}
	if e.tokensTaken() != 0 {
		t.Errorf("%d tokens taken", e.tokensTaken())
	}
	var unknown, known string
	if err := e.pool.QueryRow(ctx, `SELECT last_error FROM messages WHERE id = $1`, reclaimed).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT last_error FROM messages WHERE id = $1`, queued).Scan(&known); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unknown, "unknown") {
		t.Errorf("last_error %q; a message that died mid-attempt must say its outcome is unknown", unknown)
	}
	if known != "provider answered 429" || str(loadMessage(t, e.pool, queued).LastErrorClass) != "rate_limited" {
		t.Errorf("last_error %q; a queued message keeps the error of its last attempt", known)
	}
	if state, _ := campaignState(t, e.pool, c.ID); state != "completed_with_failures" {
		t.Errorf("campaign %s, want completed_with_failures", state)
	}
}

type failFirstBegin struct {
	*pgxpool.Pool
	failed atomic.Bool
}

func (d *failFirstBegin) Begin(ctx context.Context) (pgx.Tx, error) {
	if d.failed.CompareAndSwap(false, true) {
		return nil, errCrash
	}
	return d.Pool.Begin(ctx)
}

func TestCompletionRetriedAfterTerminalUpdate(t *testing.T) {
	e := newSendEnv(t, mockprovider.Config{})
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.worker.DB = &failFirstBegin{Pool: e.pool}

	if err := e.work(id); !errors.Is(err, errCrash) {
		t.Fatalf("first run: %v; want the completion check's error, so River retries", err)
	}
	if m := loadMessage(t, e.pool, id); m.State != "sent" {
		t.Fatalf("message %+v; want sent", m)
	}
	if state, _ := campaignState(t, e.pool, c.ID); state != "sending" {
		t.Fatalf("campaign %s after the failed check, want sending", state)
	}

	if err := e.work(id); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if state, _ := campaignState(t, e.pool, c.ID); state != "completed" {
		t.Errorf("campaign %s after the retry, want completed", state)
	}
	if n := len(e.prov.callsFor(id)); n != 1 {
		t.Errorf("%d provider calls, want 1", n)
	}
}
