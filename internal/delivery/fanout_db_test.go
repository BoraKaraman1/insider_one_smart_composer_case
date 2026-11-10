package delivery

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
)

func insertOnlyClient(t *testing.T, p *pgxpool.Pool) *river.Client[pgx.Tx] {
	t.Helper()
	c, err := river.NewClient(riverpgxv5.New(p), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newFanout(t *testing.T, p *pgxpool.Pool, asOf time.Time) *FanoutWorker {
	t.Helper()
	return &FanoutWorker{DB: p, Jobs: insertOnlyClient(t, p), Now: func() time.Time { return asOf }}
}

func fanoutJob(campaignID string) *river.Job[jobs.FanoutArgs] {
	return &river.Job[jobs.FanoutArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: 1, Kind: jobs.FanoutArgs{}.Kind(), State: rivertype.JobStateRunning},
		Args:   jobs.FanoutArgs{CampaignID: campaignID},
	}
}

func expectedAudience(t *testing.T, p *pgxpool.Pool, channel string, asOf time.Time) map[string]bool {
	t.Helper()
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	rule, res := segment.ValidateJSON(set, []byte(abandoners))
	if !res.OK() {
		t.Fatalf("fixture rule: %v", res.Errors)
	}
	q, err := segment.AudienceQuery(rule, channel, asOf)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := p.Query(context.Background(), "SELECT id::text FROM ("+q.SQL+") a", q.Args...)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

type fanoutRow struct{ ID, User, Channel, Revision, State string }

func messagesOf(t *testing.T, p *pgxpool.Pool, campaignID string) []fanoutRow {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT id::text, user_id::text, channel::text, revision_id::text, state::text
		FROM messages WHERE campaign_id = $1`, campaignID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (fanoutRow, error) {
		var m fanoutRow
		err := r.Scan(&m.ID, &m.User, &m.Channel, &m.Revision, &m.State)
		return m, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sendJobs(t *testing.T, p *pgxpool.Pool) map[string][]string {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT args->>'message_id', queue FROM river_job WHERE kind = $1`, jobs.SendArgs{}.Kind())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, queue string
		if err := rows.Scan(&id, &queue); err != nil {
			t.Fatal(err)
		}
		out[id] = append(out[id], queue)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFanout(t *testing.T) {
	p, meta := appPool(t)
	c := insertCampaignIn(t, p, "approved", 2)
	if err := newFanout(t, p, meta.AsOf).Work(context.Background(), fanoutJob(c.ID)); err != nil {
		t.Fatal(err)
	}

	msgs := messagesOf(t, p, c.ID)
	got := map[string]map[string]bool{"sms": {}, "email": {}}
	for _, m := range msgs {
		got[m.Channel][m.User] = true
		if m.Revision != c.Revision || m.State != "queued" {
			t.Errorf("message %+v; want queued on the approved revision %s", m, c.Revision)
		}
	}
	for _, ch := range []string{"sms", "email"} {
		want := expectedAudience(t, p, ch, meta.AsOf)
		if len(want) == 0 {
			t.Fatalf("%s audience is empty; the test would prove nothing", ch)
		}
		if !maps.Equal(got[ch], want) {
			t.Errorf("%s recipients: %d, want the %d the compiler selects", ch, len(got[ch]), len(want))
		}
	}
	if len(got["sms"]) != 210 {
		t.Errorf("%d SMS messages, want 210", len(got["sms"]))
	}

	js := sendJobs(t, p)
	if len(js) != len(msgs) {
		t.Errorf("%d send jobs for %d messages", len(js), len(msgs))
	}
	for _, m := range msgs {
		if q := js[m.ID]; !slices.Equal(q, []string{m.Channel}) {
			t.Errorf("message %s (%s): jobs on queues %v, want one on %s", m.ID, m.Channel, q, m.Channel)
		}
	}
	if state, version := campaignState(t, p, c.ID); state != "sending" || version != 3 {
		t.Errorf("campaign %s version %d; want sending version 3", state, version)
	}
	if got := auditTrail(t, p, c.ID); !slices.Equal(got, []string{"approved>sending"}) {
		t.Errorf("audit trail %v", got)
	}
}

func TestFanoutTwiceChangesNothing(t *testing.T) {
	p, meta := appPool(t)
	c := insertCampaignIn(t, p, "approved", 2)
	f := newFanout(t, p, meta.AsOf)
	if err := f.Work(context.Background(), fanoutJob(c.ID)); err != nil {
		t.Fatal(err)
	}
	msgs, js := messagesOf(t, p, c.ID), sendJobs(t, p)
	if err := f.Work(context.Background(), fanoutJob(c.ID)); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if again := messagesOf(t, p, c.ID); len(again) != len(msgs) {
		t.Errorf("second run: %d messages, was %d", len(again), len(msgs))
	}
	if again := sendJobs(t, p); len(again) != len(js) {
		t.Errorf("second run: jobs for %d messages, was %d", len(again), len(js))
	}
	for id, q := range sendJobs(t, p) {
		if len(q) != 1 {
			t.Errorf("message %s has %d send jobs", id, len(q))
		}
	}
	if state, version := campaignState(t, p, c.ID); state != "sending" || version != 3 {
		t.Errorf("campaign %s version %d; want sending version 3", state, version)
	}
}

func TestFanoutWithNoRecipientsCompletes(t *testing.T) {
	p, meta := appPool(t)
	c := insertCampaignIn(t, p, "approved", 2)
	if _, err := p.Exec(context.Background(), `UPDATE users SET consent_sms = false, consent_email = false`); err != nil {
		t.Fatal(err)
	}
	if err := newFanout(t, p, meta.AsOf).Work(context.Background(), fanoutJob(c.ID)); err != nil {
		t.Fatal(err)
	}
	if n := len(messagesOf(t, p, c.ID)); n != 0 {
		t.Errorf("%d messages without consent", n)
	}
	if state, _ := campaignState(t, p, c.ID); state != "completed" {
		t.Errorf("campaign %s, want completed", state)
	}
	if got := auditTrail(t, p, c.ID); !slices.Equal(got, []string{"approved>sending", "sending>completed"}) {
		t.Errorf("audit trail %v", got)
	}
}

func TestFanoutOfCancelledCampaign(t *testing.T) {
	p, meta := appPool(t)
	c := insertCampaignIn(t, p, "cancelled", 3)
	if err := newFanout(t, p, meta.AsOf).Work(context.Background(), fanoutJob(c.ID)); err != nil && !cancelled(err) {
		t.Fatalf("work: %v", err)
	}
	if n := len(messagesOf(t, p, c.ID)); n != 0 {
		t.Errorf("%d messages for a cancelled campaign", n)
	}
	if state, version := campaignState(t, p, c.ID); state != "cancelled" || version != 3 {
		t.Errorf("campaign %s version %d; want it untouched", state, version)
	}
}
