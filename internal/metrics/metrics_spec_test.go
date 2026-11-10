package metrics

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics/metricstest"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

var golden = []Series{
	{"generation_duration_seconds", "histogram", []string{"outcome"}},
	{"http_requests_total", "counter", []string{"route", "status"}},
	{"llm_request_duration_seconds", "histogram", []string{"stage"}},
	{"llm_requests_total", "counter", []string{"outcome", "stage"}},
	{"llm_tokens_total", "counter", []string{"direction", "stage"}},
	{"llm_validation_failures_total", "counter", []string{"rule", "stage"}},
	{"messages", "gauge", []string{"channel", "state"}},
	{"messages_total", "counter", []string{"channel", "outcome"}},
	{"provider_request_duration_seconds", "histogram", []string{"channel"}},
	{"provider_requests_total", "counter", []string{"channel", "class"}},
	{"queue_jobs", "gauge", []string{"queue", "state"}},
	{"queue_oldest_job_age_seconds", "gauge", []string{"queue"}},
	{"rate_limiter_wait_seconds", "histogram", []string{"channel"}},
	{"retries_total", "counter", []string{"channel", "reason"}},
	{"sse_streams", "gauge", nil},
	{"worker_last_processed_timestamp_seconds", "gauge", nil},
}

func normalized(in []Series) []Series {
	out := make([]Series, len(in))
	for i, s := range in {
		labels := slices.Clone(s.Labels)
		slices.Sort(labels)
		out[i] = Series{Name: s.Name, Kind: s.Kind, Labels: labels}
	}
	slices.SortFunc(out, func(a, b Series) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func TestCatalogIsGolden(t *testing.T) {
	got, want := normalized(Catalog()), normalized(golden)
	if len(got) != len(want) {
		t.Errorf("%d series, want %d", len(got), len(want))
	}
	for i := 0; i < len(got) && i < len(want); i++ {
		g, w := got[i], want[i]
		if g.Name != w.Name || g.Kind != w.Kind || !slices.Equal(g.Labels, w.Labels) {
			t.Errorf("series %d: %+v, want %+v", i, g, w)
		}
	}
	for _, s := range got {
		for _, l := range s.Labels {
			if strings.Contains(l, "id") {
				t.Errorf("%s has label %q: labels are closed sets, never ids", s.Name, l)
			}
		}
		if s.Kind == "counter" && !strings.HasSuffix(s.Name, "_total") {
			t.Errorf("counter %s does not end in _total", s.Name)
		}
	}
}

func TestHandlerExposesOnlyCatalog(t *testing.T) {
	r := New()
	known := map[string]bool{}
	for _, s := range Catalog() {
		known[s.Name] = true
		if s.Kind == "histogram" {
			for _, suf := range []string{"_bucket", "_sum", "_count"} {
				known[s.Name+suf] = true
			}
		}
	}
	for _, n := range metricstest.Scrape(t, r.Handler()).Names() {
		if !known[n] && !strings.HasPrefix(n, "go_") && !strings.HasPrefix(n, "process_") {
			t.Errorf("exposed series %s is not in the catalog", n)
		}
	}
}

func TestDatabaseGaugesMatchRows(t *testing.T) {
	url, _ := testdb.Seeded(t)
	ctx := context.Background()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)

	users := userIDs(t, p, 6)
	a, b := sendingCampaign(t, p), sendingCampaign(t, p)
	rows := []struct {
		c       [2]string
		user    string
		channel string
		state   string
	}{
		{a, users[0], "sms", "sent"},
		{a, users[1], "sms", "sent"},
		{a, users[2], "sms", "queued"},
		{a, users[0], "email", "failed"},
		{b, users[3], "sms", "sent"},
		{b, users[4], "email", "sent"},
		{b, users[5], "email", "dead"},
	}
	for _, r := range rows {
		var id string
		if err := p.QueryRow(ctx, `INSERT INTO messages (campaign_id, revision_id, user_id, channel) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			r.c[0], r.c[1], r.user, r.channel).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if r.state != "queued" {
			if _, err := p.Exec(ctx, `UPDATE messages SET state = $2::text, attempts = 1, first_attempt_at = now(),
				sent_at = CASE WHEN $2::text = 'sent' THEN now() END,
				provider_message_id = CASE WHEN $2::text = 'sent' THEN 'p-' || id::text END,
				last_error_class = CASE WHEN $2::text IN ('failed', 'dead') THEN 'client_error'::text END
				WHERE id = $1`, id, r.state); err != nil {
				t.Fatalf("set %s: %v", r.state, err)
			}
		}
	}

	for i := range 2 {
		if _, err := p.Exec(ctx, `INSERT INTO river_job (kind, queue, state, args, max_attempts, priority, created_at, scheduled_at)
			VALUES ('send', 'sms', 'available', '{}', 25, 1, now() - make_interval(secs => $1), now())`, 120-i*60); err != nil {
			t.Fatal(err)
		}
	}

	reg := New()
	reg.WatchDatabase(p)
	s := metricstest.Scrape(t, reg.Handler())
	for _, w := range []struct {
		channel, state string
		n              float64
	}{
		{"sms", "sent", 3}, {"sms", "queued", 1}, {"email", "sent", 1}, {"email", "failed", 1}, {"email", "dead", 1},
	} {
		if got := s.Get("messages", "channel="+w.channel, "state="+w.state); got != w.n {
			t.Errorf("messages{%s,%s} = %v, want %v", w.channel, w.state, got, w.n)
		}
	}
	if got := s.Get("queue_jobs", "queue=sms", "state=available"); got != 2 {
		t.Errorf("queue_jobs{sms,available} = %v, want 2", got)
	}
	if age := s.Get("queue_oldest_job_age_seconds", "queue=sms"); age < 120 || age > 120+30 {
		t.Errorf("queue_oldest_job_age_seconds{sms} = %v, want about 120", age)
	}
	for _, v := range s.LabelValues() {
		if metricstest.IDShaped(v) {
			t.Errorf("label value %q looks like an id", v)
		}
	}

	if _, err := p.Exec(ctx, `UPDATE messages SET state = 'sent', sent_at = now(), provider_message_id = 'p-late'
		WHERE campaign_id = $1 AND state = 'queued'`, a[0]); err != nil {
		t.Fatal(err)
	}
	s = metricstest.Scrape(t, reg.Handler())
	if got := s.Get("messages", "channel=sms", "state=sent"); got != 4 {
		t.Errorf("after an update: messages{sms,sent} = %v, want 4", got)
	}
}

func TestDatabaseGaugesTimeout(t *testing.T) {
	url, _ := testdb.Seeded(t)
	ctx := context.Background()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	lock, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err := lock.Exec(ctx, `LOCK TABLE messages IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	reg := New()
	reg.WatchDatabase(p)
	start := time.Now()
	_ = metricstest.Scrape(t, reg.Handler())
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("scrape took %v under a locked table, want about 2 s", d)
	}
}

func userIDs(t *testing.T, p *pgxpool.Pool, n int) []string {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT id::text FROM users WHERE consent_sms AND consent_email ORDER BY id LIMIT $1`, n)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) != n {
		t.Fatalf("users: %d %v", len(ids), err)
	}
	return ids
}

func sendingCampaign(t *testing.T, p *pgxpool.Pool) [2]string {
	t.Helper()
	ctx := context.Background()
	var c [2]string
	if err := p.QueryRow(ctx, `INSERT INTO campaigns (brief, campaign_url) VALUES ('b', 'https://shop.example/k') RETURNING id::text`).Scan(&c[0]); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO campaign_revisions (campaign_id, n, source, segment, sms_body, email_subject, email_body)
		VALUES ($1, 1, 'llm', '{"version":1,"match":"all","conditions":[]}', 'sms', 'subject', 'body') RETURNING id::text`, c[0]).Scan(&c[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE campaigns SET state = 'sending', version = 3, current_revision_id = $2,
		approved_revision_id = $2, approved_by = 'fixture', approved_at = now() WHERE id = $1`, c[0], c[1]); err != nil {
		t.Fatal(err)
	}
	return c
}
