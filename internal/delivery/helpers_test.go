package delivery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockprovider"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

const (
	campaignURL  = "https://shop.example/k"
	smsBody      = "Merhaba {{first_name}}, sepetin seni bekliyor: {{campaign_url}}"
	emailSubject = "{{first_name}}, sepetin seni bekliyor"
	emailBody    = "Merhaba {{first_name}} {{last_name}}, {{city}} için indirim: {{campaign_url}}"
	// The worked example: Istanbul cart abandoners (docs/seed-data.md).
	abandoners = `{"version":1,"match":"all","conditions":[
		{"kind":"enum_attribute","field":"city","op":"eq","value":"istanbul"},
		{"kind":"event","event":"cart_add","within_days":30,"negate":false},
		{"kind":"event","event":"purchase","within_days":30,"negate":true}]}`
)

// appPool opens a seeded clone of the app database.
func appPool(t *testing.T) (*pgxpool.Pool, testdb.Seed) {
	t.Helper()
	url, meta := testdb.Seeded(t)
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p, meta
}

// call is one request the provider received.
type call struct {
	Key    string
	Chaos  string // the X-Chaos the test forced, "" for none
	Body   map[string]any
	Status int // 0 when the connection was dropped or the client left
}

// provider runs the real mock provider on its own database, behind a thin
// wrapper that records every request and can force outcomes per key with
// X-Chaos. Hooks let a test act while a request is in flight.
type provider struct {
	URL   string
	inner http.Handler
	srv   *httptest.Server

	mu            sync.Mutex
	script        map[string][]string
	calls         []call
	onRequest     func(key string) // before the provider handles the request
	afterResponse func(key string) // after it wrote its answer
}

func newProvider(t *testing.T, cfg mockprovider.Config) *provider {
	t.Helper()
	ctx := context.Background()
	p, err := pgxpool.New(ctx, testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if err := mockprovider.Migrate(ctx, p); err != nil {
		t.Fatal(err)
	}
	cfg.ChaosHeader = true
	pr := &provider{inner: mockprovider.New(p, cfg), script: map[string][]string{}}
	pr.srv = httptest.NewServer(pr)
	t.Cleanup(pr.srv.Close)
	pr.URL = pr.srv.URL
	return pr
}

// force sets the X-Chaos of the next requests for key, in order. Requests
// past the script get no header ("" means the provider's own chaos).
func (p *provider) force(key string, chaos ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.script[key] = append(p.script[key], chaos...)
}

func (p *provider) hooks(onRequest, afterResponse func(key string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onRequest, p.afterResponse = onRequest, afterResponse
}

func (p *provider) all() []call {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]call(nil), p.calls...)
}

func (p *provider) callsFor(key string) []call {
	var out []call
	for _, c := range p.all() {
		if c.Key == key {
			out = append(out, c)
		}
	}
	return out
}

func (p *provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/messages" {
		p.inner.ServeHTTP(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	key := r.Header.Get("Idempotency-Key")
	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)

	p.mu.Lock()
	chaos := ""
	if s := p.script[key]; len(s) > 0 {
		chaos, p.script[key] = s[0], s[1:]
	}
	i := len(p.calls)
	p.calls = append(p.calls, call{Key: key, Chaos: chaos, Body: decoded})
	before, after := p.onRequest, p.afterResponse
	p.mu.Unlock()

	r.Header.Del("X-Chaos") // only the test decides
	if chaos != "" {
		r.Header.Set("X-Chaos", chaos)
	}
	if before != nil {
		before(key)
	}
	rec := &statusRecorder{ResponseWriter: w}
	p.inner.ServeHTTP(rec, r)
	p.mu.Lock()
	p.calls[i].Status = rec.status
	p.mu.Unlock()
	if after != nil {
		after(key)
	}
}

func (p *provider) stats(t *testing.T) mockprovider.Stats {
	t.Helper()
	resp, err := p.srv.Client().Get(p.URL + "/v1/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s mockprovider.Stats
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// statusRecorder notes the status and still lets the provider hijack the
// connection for the drop mode.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(r.ResponseWriter).Hijack()
}

func (r *statusRecorder) Flush() { _ = http.NewResponseController(r.ResponseWriter).Flush() }

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// countingLimiter hands out tokens at once and counts them.
type countingLimiter struct{ n atomic.Int64 }

func (c *countingLimiter) Wait(ctx context.Context) error {
	c.n.Add(1)
	return ctx.Err()
}

var errCrash = errors.New("injected crash: the database is gone")

// flakyDB is the app database until down is set. Then every call fails, as
// if the worker process died. It injects a crash between the provider call
// and the state update without a hook in the worker.
type flakyDB struct {
	*pgxpool.Pool
	down atomic.Bool
}

func (d *flakyDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if d.down.Load() {
		return pgconn.CommandTag{}, errCrash
	}
	return d.Pool.Exec(ctx, sql, args...)
}

func (d *flakyDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if d.down.Load() {
		return nil, errCrash
	}
	return d.Pool.Query(ctx, sql, args...)
}

func (d *flakyDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if d.down.Load() {
		return errRow{}
	}
	return d.Pool.QueryRow(ctx, sql, args...)
}

func (d *flakyDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if d.down.Load() {
		return nil, errCrash
	}
	return d.Pool.Begin(ctx)
}

type errRow struct{}

func (errRow) Scan(...any) error { return errCrash }

// sendEnv is a seeded app database, a mock provider and a send worker with
// a fixed clock, a fixed backoff and counting limiters.
type sendEnv struct {
	pool    *pgxpool.Pool
	asOf    time.Time
	now     time.Time
	prov    *provider
	tokens  map[string]*countingLimiter
	worker  *SendWorker
	logs    *bytes.Buffer
	retries []int // the retry numbers Backoff was called with
	mu      sync.Mutex
}

// fixedBackoff is what the test Backoff returns, so snoozes are exact.
const fixedBackoff = 3 * time.Second

func newSendEnv(t *testing.T, cfg mockprovider.Config) *sendEnv {
	t.Helper()
	pool, meta := appPool(t)
	e := &sendEnv{
		pool:   pool,
		asOf:   meta.AsOf,
		now:    meta.AsOf.Add(time.Hour),
		prov:   newProvider(t, cfg),
		tokens: map[string]*countingLimiter{"sms": {}, "email": {}},
		logs:   &bytes.Buffer{},
	}
	e.worker = &SendWorker{
		DB:              pool,
		ProviderURL:     e.prov.URL,
		Limiters:        map[string]Limiter{"sms": e.tokens["sms"], "email": e.tokens["email"]},
		ProviderTimeout: 300 * time.Millisecond,
		Now:             func() time.Time { return e.now },
		Backoff: func(retry int) time.Duration {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.retries = append(e.retries, retry)
			return fixedBackoff
		},
		Log: slog.New(slog.NewJSONHandler(&lockedWriter{w: e.logs}, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	return e
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

func (e *sendEnv) tokensTaken() int64 { return e.tokens["sms"].n.Load() + e.tokens["email"].n.Load() }

type camp struct{ ID, Revision string }

// insertSendingCampaign creates a campaign that is approved and sending,
// with one revision.
func insertSendingCampaign(t *testing.T, p *pgxpool.Pool) camp {
	t.Helper()
	return insertCampaignIn(t, p, "sending", 3)
}

// insertCampaignIn creates a campaign in a state with an approved revision
// of the abandoners rule and the test copy.
func insertCampaignIn(t *testing.T, p *pgxpool.Pool, state string, version int) camp {
	t.Helper()
	ctx := context.Background()
	var c camp
	if err := p.QueryRow(ctx, `INSERT INTO campaigns (brief, campaign_url) VALUES ('brief', $1) RETURNING id::text`, campaignURL).Scan(&c.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO campaign_revisions (campaign_id, n, source, segment, sms_body, email_subject, email_body,
		audience_total, sms_eligible, email_eligible)
		VALUES ($1, 1, 'llm', $2, $3, $4, $5, 315, 210, 250) RETURNING id::text`,
		c.ID, abandoners, smsBody, emailSubject, emailBody).Scan(&c.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE campaigns SET state = $2, version = $3, current_revision_id = $4,
		approved_revision_id = $4, approved_by = 'fixture', approved_at = now() WHERE id = $1`, c.ID, state, version, c.Revision); err != nil {
		t.Fatal(err)
	}
	return c
}

// consentingUsers returns n users with both consents and a non-empty name.
func consentingUsers(t *testing.T, p *pgxpool.Pool, n int) []string {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT id::text FROM users
		WHERE consent_sms AND consent_email AND first_name <> '' ORDER BY id LIMIT $1`, n)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) != n {
		t.Fatalf("need %d consenting users, got %d (%v)", n, len(ids), err)
	}
	return ids
}

// insertMessage adds a queued message. extra is an optional SET clause.
func insertMessage(t *testing.T, p *pgxpool.Pool, c camp, user, channel, extra string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := p.QueryRow(ctx, `INSERT INTO messages (campaign_id, revision_id, user_id, channel)
		VALUES ($1, $2, $3, $4) RETURNING id::text`, c.ID, c.Revision, user, channel).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if extra != "" {
		if _, err := p.Exec(ctx, `UPDATE messages SET `+extra+` WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

type msgRow struct {
	State          string
	Attempts       int
	LastErrorClass *string
	NextAttemptAt  *time.Time
	FirstAttemptAt *time.Time
	ProviderID     *string
	SentAt         *time.Time
}

func loadMessage(t *testing.T, p *pgxpool.Pool, id string) msgRow {
	t.Helper()
	var m msgRow
	err := p.QueryRow(context.Background(), `SELECT state::text, attempts, last_error_class::text, next_attempt_at,
		first_attempt_at, provider_message_id, sent_at FROM messages WHERE id = $1`, id).
		Scan(&m.State, &m.Attempts, &m.LastErrorClass, &m.NextAttemptAt, &m.FirstAttemptAt, &m.ProviderID, &m.SentAt)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func campaignState(t *testing.T, p *pgxpool.Pool, id string) (string, int) {
	t.Helper()
	var state string
	var version int
	if err := p.QueryRow(context.Background(), `SELECT state::text, version FROM campaigns WHERE id = $1`, id).Scan(&state, &version); err != nil {
		t.Fatal(err)
	}
	return state, version
}

func auditTrail(t *testing.T, p *pgxpool.Pool, id string) []string {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT coalesce(from_state::text, '') || '>' || to_state::text
		FROM campaign_events WHERE campaign_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sendJob(messageID string) *river.Job[jobs.SendArgs] {
	return &river.Job[jobs.SendArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: 1, Kind: jobs.SendArgs{}.Kind(), State: rivertype.JobStateRunning},
		Args:   jobs.SendArgs{MessageID: messageID},
	}
}

func (e *sendEnv) work(messageID string) error {
	return e.worker.Work(context.Background(), sendJob(messageID))
}

// snoozed reports the delay of a river.JobSnooze error.
func snoozed(err error) (time.Duration, bool) {
	var s *river.JobSnoozeError
	if errors.As(err, &s) {
		return s.Duration, true
	}
	return 0, false
}

func cancelled(err error) bool {
	var c *river.JobCancelError
	return errors.As(err, &c)
}
