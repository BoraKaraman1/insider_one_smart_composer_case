package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockllm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/retry"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

const exampleBrief = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder."

const goodCopy = `{"sms":{"body":"Merhaba {{first_name}}, sepetin seni bekliyor: {{campaign_url}}"},` +
	`"email":{"subject":"Sepetindeki ürünler seni bekliyor","body":"Merhaba {{first_name}}, %30 indirim için: {{campaign_url}}"}}`

func copyJSON(sms, subject, body string) string {
	b, _ := json.Marshal(map[string]any{"sms": map[string]string{"body": sms}, "email": map[string]string{"subject": subject, "body": body}})
	return string(b)
}

func genPool(t *testing.T) (*pgxpool.Pool, testdb.Seed) {
	t.Helper()
	url, meta := testdb.Seeded(t)
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p, meta
}

type sleepLog struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (s *sleepLog) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.delays = append(s.delays, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleepLog) list() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.delays)
}

func newGen(t *testing.T, p *pgxpool.Pool, asOf time.Time, o mockllm.Options) (*Generator, *mockllm.Server, *sleepLog) {
	t.Helper()
	m := mockllm.New(o)
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	cl, err := llm.New(llm.Config{
		BaseURL:      srv.URL,
		APIKey:       "test-placeholder",
		Schemas:      schemas(t),
		StallTimeout: 300 * time.Millisecond,
		TotalTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	sl := &sleepLog{}
	g := &Generator{
		Pool:          p,
		Schemas:       schemas(t),
		LLM:           cl,
		Now:           func() time.Time { return asOf },
		Rand:          rand.New(rand.NewPCG(1, 2)),
		Sleep:         sl.sleep,
		DeltaInterval: 0,
	}
	return g, m, sl
}

func insertGenerating(t *testing.T, p *pgxpool.Pool, brief string, version int) string {
	t.Helper()
	var id string
	err := p.QueryRow(context.Background(), `INSERT INTO campaigns (brief, campaign_url, state, version)
		VALUES ($1, 'https://shop.example/k', 'generating', $2) RETURNING id::text`, brief, version).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type llmCall struct {
	Stage, Outcome, ErrorClass, StopReason, Model, Raw, PromptVersion string
	Attempt, InputTokens, OutputTokens, LatencyMS                     int
	Errors                                                            []map[string]any
}

func llmCalls(t *testing.T, p *pgxpool.Pool, id string) []llmCall {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT stage, attempt, outcome, coalesce(error_class::text, ''), coalesce(stop_reason, ''),
			model, coalesce(input_tokens, -1), coalesce(output_tokens, -1), latency_ms, coalesce(raw_output, ''),
			validation_errors::text, coalesce(prompt_version, '')
		FROM llm_calls WHERE campaign_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (llmCall, error) {
		var c llmCall
		var errs string
		err := r.Scan(&c.Stage, &c.Attempt, &c.Outcome, &c.ErrorClass, &c.StopReason, &c.Model, &c.InputTokens, &c.OutputTokens,
			&c.LatencyMS, &c.Raw, &errs, &c.PromptVersion)
		if err == nil {
			err = json.Unmarshal([]byte(errs), &c.Errors)
		}
		return c, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func outcomes(calls []llmCall) []string {
	var out []string
	for _, c := range calls {
		s := c.Stage + " " + strconv.Itoa(c.Attempt) + " " + c.Outcome
		if c.ErrorClass != "" {
			s += " " + c.ErrorClass
		}
		out = append(out, s)
	}
	return out
}

func hasCode(errs []map[string]any, code string) bool {
	return slices.ContainsFunc(errs, func(e map[string]any) bool { return e["code"] == code })
}

type genEvent struct {
	ID      int64
	Type    string
	Payload map[string]any
}

func genEvents(t *testing.T, p *pgxpool.Pool, id string) []genEvent {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT id, type, payload::text FROM generation_events WHERE campaign_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (genEvent, error) {
		var e genEvent
		var payload string
		err := r.Scan(&e.ID, &e.Type, &payload)
		if err == nil {
			err = json.Unmarshal([]byte(payload), &e.Payload)
		}
		return e, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func eventTypes(events []genEvent) []string {
	var out []string
	for _, e := range events {
		if len(out) > 0 && e.Type == "delta" && out[len(out)-1] == "delta" {
			continue
		}
		out = append(out, e.Type)
	}
	return out
}

func rebuild(events []genEvent, attempt int) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		if e.Type != "delta" || e.Payload["attempt"] != float64(attempt) {
			continue
		}
		f, _ := e.Payload["field"].(string)
		s, _ := e.Payload["text"].(string)
		out[f] += s
	}
	return out
}

type genState struct {
	State     State
	Version   int
	Revisions int
	Audit     []string
}

func loadGen(t *testing.T, p *pgxpool.Pool, id string) genState {
	t.Helper()
	ctx := context.Background()
	var g genState
	var state string
	if err := p.QueryRow(ctx, `SELECT state::text, version, (SELECT count(*) FROM campaign_revisions WHERE campaign_id = c.id)
		FROM campaigns c WHERE id = $1`, id).Scan(&state, &g.Version, &g.Revisions); err != nil {
		t.Fatal(err)
	}
	g.State = State(state)
	rows, err := p.Query(ctx, `SELECT coalesce(from_state::text, '') || '>' || to_state::text || ' ' || actor
		FROM campaign_events WHERE campaign_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	g.Audit, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func recorded(t *testing.T, m *mockllm.Server, stage mockllm.Stage) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range m.Requests() {
		if r.Stage != stage {
			continue
		}
		var b map[string]any
		if err := json.Unmarshal(r.Body, &b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

func turns(body map[string]any) []string {
	msgs, _ := body["messages"].([]any)
	var out []string
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		var sb strings.Builder
		switch c := mm["content"].(type) {
		case string:
			sb.WriteString(c)
		case []any:
			for _, b := range c {
				bm, _ := b.(map[string]any)
				if bm["type"] == "text" {
					s, _ := bm["text"].(string)
					sb.WriteString(s)
				}
			}
		}
		out = append(out, mm["role"].(string)+": "+sb.String())
	}
	return out
}

func exampleRule(t *testing.T) segment.Rule {
	t.Helper()
	b, err := os.ReadFile("../../docs/examples/istanbul_cart_abandoners.json")
	if err != nil {
		t.Fatal(err)
	}
	r, res := segment.ValidateJSON(schemas(t), b)
	if !res.OK() {
		t.Fatal(res.Errors)
	}
	return r
}

func run(t *testing.T, g *Generator, id string, version int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := g.Run(ctx, jobs.GenerateArgs{CampaignID: id, Version: version}); err != nil {
		t.Fatalf("Run returned %v; a failed generation is an outcome, not a job error", err)
	}
}

func TestGenerateHappyPath(t *testing.T) {
	p, meta := genPool(t)
	g, m, _ := newGen(t, p, meta.AsOf, mockllm.Options{})
	id := insertGenerating(t, p, exampleBrief, 3)
	run(t, g, id, 3)

	got := loadGen(t, p, id)
	if got.State != InReview || got.Version != 4 || got.Revisions != 1 {
		t.Fatalf("campaign %+v; want in_review at version 4 with one revision", got)
	}
	if !slices.Equal(got.Audit, []string{"generating>in_review " + WorkerActor}) {
		t.Errorf("audit %v", got.Audit)
	}

	calls := llmCalls(t, p, id)
	if want := []string{"segment 1 valid", "content 1 valid"}; !slices.Equal(outcomes(calls), want) {
		t.Fatalf("llm_calls %v, want %v", outcomes(calls), want)
	}
	for _, c := range calls {
		stage := llm.Segment
		if c.Stage == "content" {
			stage = llm.Content
		}
		if c.Model != llm.DefaultModel || c.StopReason != "end_turn" || c.InputTokens <= 0 || c.OutputTokens <= 0 ||
			c.LatencyMS < 0 || c.Raw == "" || len(c.Errors) != 0 || c.PromptVersion != llm.PromptVersion(stage) {
			t.Errorf("llm_calls row %+v", c)
		}
	}

	var (
		source, segJSON, sms, subject, body, current string
		total, smsOK, emailOK                        int
	)
	err := p.QueryRow(context.Background(), `SELECT r.source, r.segment::text, r.sms_body, r.email_subject, r.email_body,
			r.audience_total, r.sms_eligible, r.email_eligible, c.current_revision_id::text
		FROM campaign_revisions r JOIN campaigns c ON c.id = r.campaign_id WHERE r.campaign_id = $1`, id).
		Scan(&source, &segJSON, &sms, &subject, &body, &total, &smsOK, &emailOK, &current)
	if err != nil {
		t.Fatal(err)
	}
	rule, res := segment.ValidateJSON(schemas(t), []byte(segJSON))
	if !res.OK() || !reflect.DeepEqual(rule, exampleRule(t)) {
		t.Errorf("stored rule %s (%+v), want the documented example", segJSON, res.Errors)
	}
	if source != "llm" || total != 315 || smsOK != 210 || emailOK <= 0 || emailOK > total {
		t.Errorf("revision source %s, counts %d/%d/%d; docs/seed-data.md says 315 total, 210 SMS", source, total, smsOK, emailOK)
	}
	cont, res := content.ValidateJSON(schemas(t), []byte(calls[1].Raw))
	if !res.OK() || cont.SMSBody != sms || cont.EmailSubject != subject || cont.EmailBody != body {
		t.Errorf("stored copy is not the model's answer as validated: %q %q %q", sms, subject, body)
	}

	events := genEvents(t, p, id)
	if want := []string{"segment_ready", "delta", "done"}; !slices.Equal(eventTypes(events), want) {
		t.Fatalf("events %v, want %v", eventTypes(events), want)
	}
	ready := events[0].Payload
	readyRule, _ := json.Marshal(ready["rule"])
	if r, res := segment.ValidateJSON(schemas(t), readyRule); !res.OK() || !reflect.DeepEqual(r, rule) {
		t.Errorf("segment_ready rule %s", readyRule)
	}
	if ready["description"] != segment.Describe(rule) {
		t.Errorf("segment_ready description %q, want segment.Describe: %q", ready["description"], segment.Describe(rule))
	}
	counts, _ := ready["counts"].(map[string]any)
	if counts["total"] != 315.0 || counts["sms_eligible"] != 210.0 || counts["email_eligible"] != float64(emailOK) {
		t.Errorf("segment_ready counts %v", counts)
	}
	want := map[string]string{"sms.body": sms, "email.subject": subject, "email.body": body}
	if got := rebuild(events, 1); !reflect.DeepEqual(got, want) {
		t.Errorf("deltas rebuild %q, want %q", got, want)
	}
	if done := events[len(events)-1].Payload; done["revision_id"] != current {
		t.Errorf("done %v, want revision_id %s", done, current)
	}

	contentReqs := recorded(t, m, mockllm.Content)
	if len(contentReqs) != 1 {
		t.Fatalf("%d content requests", len(contentReqs))
	}
	first := turns(contentReqs[0])[0]
	if !strings.Contains(first, segment.Describe(rule)) || !strings.Contains(first, "<brief>"+exampleBrief+"</brief>") {
		t.Errorf("content request lacks the description or the brief: %q", first)
	}
}

func TestGenerateMergesDeltas(t *testing.T) {
	p, meta := genPool(t)
	g, m, _ := newGen(t, p, meta.AsOf, mockllm.Options{})
	g.DeltaInterval = time.Hour
	m.Script(mockllm.Content, mockllm.Reply{Text: goodCopy})
	id := insertGenerating(t, p, exampleBrief, 0)
	run(t, g, id, 0)
	events := genEvents(t, p, id)
	perField := map[string]int{}
	for _, e := range events {
		if e.Type == "delta" {
			f, _ := e.Payload["field"].(string)
			perField[f]++
		}
	}
	for f, n := range perField {
		if n > 1 {
			t.Errorf("%s: %d delta events within one interval, want 1", f, n)
		}
	}
	want, _ := content.ValidateJSON(schemas(t), []byte(goodCopy))
	if got := rebuild(events, 1); got["sms.body"] != want.SMSBody || got["email.subject"] != want.EmailSubject || got["email.body"] != want.EmailBody {
		t.Errorf("merged deltas rebuild %q", got)
	}
}

func TestGenerateRepairLoop(t *testing.T) {
	p, meta := genPool(t)
	g, m, _ := newGen(t, p, meta.AsOf, mockllm.Options{})
	bad := copyJSON("Merhaba {{password}} {{campaign_url}}", "Sepet", "Merhaba {{first_name}} {{campaign_url}}")
	m.Script(mockllm.Content, mockllm.Reply{Text: bad}, mockllm.Reply{Text: goodCopy})
	id := insertGenerating(t, p, exampleBrief, 0)
	run(t, g, id, 0)

	if got := loadGen(t, p, id); got.State != InReview || got.Revisions != 1 {
		t.Fatalf("campaign %+v; want in_review with one revision", got)
	}
	calls := llmCalls(t, p, id)
	if want := []string{"segment 1 valid", "content 1 invalid", "content 2 valid"}; !slices.Equal(outcomes(calls), want) {
		t.Fatalf("llm_calls %v, want %v", outcomes(calls), want)
	}
	if calls[1].Raw != bad || !hasCode(calls[1].Errors, "unknown_variable") {
		t.Errorf("invalid row: raw %q, errors %v", calls[1].Raw, calls[1].Errors)
	}

	reqs := recorded(t, m, mockllm.Content)
	if len(reqs) != 2 {
		t.Fatalf("%d content requests, want 2", len(reqs))
	}
	first, second := turns(reqs[0]), turns(reqs[1])
	if len(second) != 3 || second[0] != first[0] || second[1] != "assistant: "+bad || !strings.HasPrefix(second[2], "user: ") {
		t.Fatalf("repair request turns %q", second)
	}
	repair := second[2]
	i, j := strings.Index(repair, "["), strings.LastIndex(repair, "]")
	var sent []map[string]any
	if i < 0 || j < i || json.Unmarshal([]byte(repair[i:j+1]), &sent) != nil {
		t.Fatalf("repair turn has no JSON errors: %q", repair)
	}
	if !reflect.DeepEqual(normalizeIssues(t, sent), normalizeIssues(t, calls[1].Errors)) {
		t.Errorf("repair turn errors %v, want exactly the stored errors %v", sent, calls[1].Errors)
	}

	events := genEvents(t, p, id)
	if want := []string{"segment_ready", "delta", "attempt_failed", "delta", "done"}; !slices.Equal(eventTypes(events), want) {
		t.Fatalf("events %v, want %v", eventTypes(events), want)
	}
	for _, e := range events {
		if e.Type == "attempt_failed" {
			errs, _ := json.Marshal(e.Payload["errors"])
			if e.Payload["stage"] != "content" || e.Payload["attempt"] != 1.0 || !strings.Contains(string(errs), "unknown_variable") {
				t.Errorf("attempt_failed %v", e.Payload)
			}
		}
	}
	want, _ := content.ValidateJSON(schemas(t), []byte(goodCopy))
	if got := rebuild(events, 2); got["sms.body"] != want.SMSBody || got["email.body"] != want.EmailBody {
		t.Errorf("attempt 2 deltas rebuild %q", got)
	}
}

func normalizeIssues(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func TestGenerateOutcomes(t *testing.T) {
	p, meta := genPool(t)
	failures := slices.Repeat([]mockllm.Reply{mockllm.Status(503)}, MaxTransportRetries+1)
	exhausted := []string{"segment 1 valid"}
	for range MaxTransportRetries + 1 {
		exhausted = append(exhausted, "content 1 transport_error server_error")
	}
	cases := []struct {
		name     string
		stage    mockllm.Stage
		replies  []mockllm.Reply
		state    State
		reason   string
		calls    []string
		requests int
		sleeps   int
	}{
		{"refusal is not retried", mockllm.Content, []mockllm.Reply{mockllm.Refusal(), {Text: goodCopy}}, GenerationFailed, "refusal",
			[]string{"segment 1 valid", "content 1 refusal"}, 1, 0},
		{"max_tokens is retried", mockllm.Content, []mockllm.Reply{mockllm.MaxTokens(`{"sms":{"body":"Mer`), {Text: goodCopy}}, InReview, "",
			[]string{"segment 1 valid", "content 1 max_tokens", "content 2 valid"}, 2, 0},
		{"529 is a transport retry", mockllm.Content, []mockllm.Reply{mockllm.Status(529), {Text: goodCopy}}, InReview, "",
			[]string{"segment 1 valid", "content 1 transport_error server_error", "content 1 valid"}, 2, 1},
		{"529 in the segment stage", mockllm.Segment, []mockllm.Reply{mockllm.Status(529)}, InReview, "",
			[]string{"segment 1 transport_error server_error", "segment 1 valid", "content 1 valid"}, 2, 1},
		{"429 is a transport retry", mockllm.Content, []mockllm.Reply{mockllm.RateLimited("2"), {Text: goodCopy}}, InReview, "",
			[]string{"segment 1 valid", "content 1 transport_error rate_limited", "content 1 valid"}, 2, 1},
		{"a stall trips the watchdog and is retried", mockllm.Content, []mockllm.Reply{mockllm.Stall(`{"sms":{"body":"Mer`), {Text: goodCopy}}, InReview, "",
			[]string{"segment 1 valid", "content 1 transport_error timeout", "content 1 valid"}, 2, 1},
		{"400 is never retried", mockllm.Content, []mockllm.Reply{mockllm.Status(400), {Text: goodCopy}}, GenerationFailed, "transport_error",
			[]string{"segment 1 valid", "content 1 transport_error client_error"}, 1, 0},
		{"401 is never retried", mockllm.Content, []mockllm.Reply{mockllm.Status(401), {Text: goodCopy}}, GenerationFailed, "transport_error",
			[]string{"segment 1 valid", "content 1 transport_error client_error"}, 1, 0},
		{"transport retries run out", mockllm.Content, failures, GenerationFailed, "transport_error",
			exhausted, MaxTransportRetries + 1, MaxTransportRetries},
		{"transport failures do not use up validation attempts", mockllm.Content, []mockllm.Reply{
			{Text: "not json"}, mockllm.Status(529), {Text: "not json"}, {Text: "not json"}, {Text: goodCopy},
		}, GenerationFailed, "invalid_output",
			[]string{"segment 1 valid", "content 1 invalid", "content 2 transport_error server_error", "content 2 invalid", "content 3 invalid"}, 4, 1},
	}
	for _, c := range cases {
		g, m, sl := newGen(t, p, meta.AsOf, mockllm.Options{})
		m.Script(c.stage, c.replies...)
		id := insertGenerating(t, p, exampleBrief, 0)
		run(t, g, id, 0)

		got := loadGen(t, p, id)
		if got.State != c.state {
			t.Errorf("%s: state %s, want %s", c.name, got.State, c.state)
		}
		if gotCalls := outcomes(llmCalls(t, p, id)); !slices.Equal(gotCalls, c.calls) {
			t.Errorf("%s: llm_calls %v, want %v", c.name, gotCalls, c.calls)
		}
		if n := len(recorded(t, m, c.stage)); n != c.requests {
			t.Errorf("%s: %d %s requests, want %d", c.name, n, c.stage, c.requests)
		}
		if n := len(sl.list()); n != c.sleeps {
			t.Errorf("%s: %d backoff sleeps, want %d", c.name, n, c.sleeps)
		}
		events := genEvents(t, p, id)
		last := events[len(events)-1]
		switch c.state {
		case InReview:
			if last.Type != "done" || got.Revisions != 1 {
				t.Errorf("%s: last event %s, %d revisions", c.name, last.Type, got.Revisions)
			}
		case GenerationFailed:
			if last.Type != "failed" || last.Payload["reason"] != c.reason || got.Revisions != 0 {
				t.Errorf("%s: last event %+v, %d revisions; want failed with reason %s and no revision", c.name, last, got.Revisions, c.reason)
			}
			if want := "generating>generation_failed " + WorkerActor; !slices.Contains(got.Audit, want) {
				t.Errorf("%s: audit %v, want %q", c.name, got.Audit, want)
			}
		}
	}
}

func TestGenerateBackoffDelays(t *testing.T) {
	p, meta := genPool(t)
	g, m, sl := newGen(t, p, meta.AsOf, mockllm.Options{})
	m.Script(mockllm.Content, mockllm.RateLimited("2"), mockllm.Status(529), mockllm.Reply{Text: goodCopy})
	id := insertGenerating(t, p, exampleBrief, 0)
	run(t, g, id, 0)
	d := sl.list()
	if len(d) != 2 || d[0] != 2*time.Second || d[1] < 0 || d[1] > 2*retry.BaseDelay {
		t.Errorf("delays %v; want 2s from Retry-After, then a jittered backoff of at most %v", d, 2*retry.BaseDelay)
	}
}

func TestGenerateStaleJobDoesNothing(t *testing.T) {
	p, meta := genPool(t)
	cases := []struct {
		name    string
		state   State
		version int
	}{
		{"older version", Generating, 4},
		{"newer version", Generating, 6},
		{"in review", InReview, 5},
		{"cancelled", Cancelled, 5},
		{"failed", GenerationFailed, 5},
		{"draft", Draft, 5},
	}
	for _, c := range cases {
		g, m, _ := newGen(t, p, meta.AsOf, mockllm.Options{})
		id := insertGenerating(t, p, exampleBrief, 5)
		if _, err := p.Exec(context.Background(), `UPDATE campaigns SET state = $2 WHERE id = $1`, id, string(c.state)); err != nil {
			t.Fatal(err)
		}
		run(t, g, id, c.version)
		if n := len(m.Requests()); n != 0 {
			t.Errorf("%s: %d model requests, want 0", c.name, n)
		}
		if got := loadGen(t, p, id); got.State != c.state || got.Version != 5 || got.Revisions != 0 || len(got.Audit) != 0 {
			t.Errorf("%s: campaign changed: %+v", c.name, got)
		}
		if n := len(llmCalls(t, p, id)) + len(genEvents(t, p, id)); n != 0 {
			t.Errorf("%s: %d rows written by a stale job", c.name, n)
		}
	}
}

func TestCancelDuringGeneration(t *testing.T) {
	p, meta := genPool(t)
	g, m, _ := newGen(t, p, meta.AsOf, mockllm.Options{})
	m.Script(mockllm.Content, mockllm.Stall(`{"sms":{"body":"Mer`), mockllm.Reply{Text: goodCopy})
	id := insertGenerating(t, p, exampleBrief, 0)

	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background(), jobs.GenerateArgs{CampaignID: id, Version: 0}) }()
	deadline := time.Now().Add(10 * time.Second)
	for len(recorded(t, m, mockllm.Content)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the content stage never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := Cancel(context.Background(), p, id, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop after the cancel")
	}

	if got := loadGen(t, p, id); got.State != Cancelled || got.Revisions != 0 {
		t.Errorf("campaign %+v; want cancelled with no revision", got)
	}
	if n := len(recorded(t, m, mockllm.Content)); n != 1 {
		t.Errorf("%d content requests; the stalled call was retried after the cancel", n)
	}
	events := genEvents(t, p, id)
	last := events[len(events)-1]
	if last.Type != "failed" || last.Payload["reason"] != "cancelled" {
		t.Errorf("last event %+v; want failed with reason cancelled and nothing after it", last)
	}
	if slices.ContainsFunc(events, func(e genEvent) bool { return e.Type == "done" }) {
		t.Error("a done event was written for a cancelled campaign")
	}
}

type failingInserter struct{}

func (failingInserter) InsertTx(context.Context, pgx.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("queue unavailable")
}

type jobRow struct {
	Kind, Queue string
	Args        jobs.GenerateArgs
}

func generateJobs(t *testing.T, p *pgxpool.Pool) []jobRow {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT kind, queue, args::text FROM river_job WHERE kind = 'generate' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (jobRow, error) {
		var j jobRow
		var args string
		err := r.Scan(&j.Kind, &j.Queue, &args)
		if err == nil {
			err = json.Unmarshal([]byte(args), &j.Args)
		}
		return j, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStartGeneration(t *testing.T) {
	p, _ := genPool(t)
	ctx := context.Background()
	rc, err := river.NewClient(riverpgxv5.New(p), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var want []jobRow
	for _, from := range []State{Draft, InReview, GenerationFailed} {
		id := insertGenerating(t, p, exampleBrief, 7)
		if _, err := p.Exec(ctx, `UPDATE campaigns SET state = $2 WHERE id = $1`, id, string(from)); err != nil {
			t.Fatal(err)
		}
		args, err := StartGeneration(ctx, p, rc, id, 7, "admin")
		if err != nil {
			t.Fatalf("from %s: %v", from, err)
		}
		if args != (jobs.GenerateArgs{CampaignID: id, Version: 8}) {
			t.Errorf("from %s: args %+v, want version 8", from, args)
		}
		want = append(want, jobRow{Kind: "generate", Queue: jobs.QueueGenerate, Args: args})
		got := loadGen(t, p, id)
		if got.State != Generating || got.Version != 8 || !slices.Equal(got.Audit, []string{string(from) + ">generating admin"}) {
			t.Errorf("from %s: campaign %+v", from, got)
		}
		events := genEvents(t, p, id)
		if len(events) != 1 || events[0].Type != "started" || events[0].Payload["version"] != 8.0 {
			t.Errorf("from %s: events %+v; want one started event", from, events)
		}
	}
	if got := generateJobs(t, p); !reflect.DeepEqual(got, want) {
		t.Errorf("jobs %+v, want %+v", got, want)
	}

	refused := []struct {
		name    string
		state   State
		version int
		err     error
	}{
		{"stale version", Draft, 6, ErrConflict},
		{"already generating", Generating, 7, ErrInvalidTransition},
		{"approved", Approved, 7, ErrInvalidTransition},
		{"cancelled", Cancelled, 7, ErrInvalidTransition},
	}
	for _, c := range refused {
		id := insertGenerating(t, p, exampleBrief, 7)
		if c.state != Approved {
			if _, err := p.Exec(ctx, `UPDATE campaigns SET state = $2 WHERE id = $1`, id, string(c.state)); err != nil {
				t.Fatal(err)
			}
		} else {
			var rev string
			if err := p.QueryRow(ctx, `INSERT INTO campaign_revisions (campaign_id, n, source, segment, sms_body, email_subject, email_body)
				VALUES ($1, 1, 'llm', '{}', 's', 's', 'b') RETURNING id::text`, id).Scan(&rev); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Exec(ctx, `UPDATE campaigns SET state = 'approved', current_revision_id = $2, approved_revision_id = $2, approved_by = 'x', approved_at = now() WHERE id = $1`, id, rev); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := StartGeneration(ctx, p, rc, id, c.version, "admin"); !errors.Is(err, c.err) {
			t.Errorf("%s: error %v, want %v", c.name, err, c.err)
		}
		if got := loadGen(t, p, id); got.State != c.state || got.Version != 7 || len(got.Audit) != 0 {
			t.Errorf("%s: campaign changed: %+v", c.name, got)
		}
		if n := len(genEvents(t, p, id)); n != 0 {
			t.Errorf("%s: %d events written", c.name, n)
		}
	}
	if n := len(generateJobs(t, p)); n != len(want) {
		t.Errorf("%d jobs after refused starts, want %d", n, len(want))
	}

	id := insertGenerating(t, p, exampleBrief, 0)
	if _, err := p.Exec(ctx, `UPDATE campaigns SET state = 'draft' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := StartGeneration(ctx, p, failingInserter{}, id, 0, "admin"); err == nil {
		t.Fatal("StartGeneration succeeded although the job insert failed")
	}
	if got := loadGen(t, p, id); got.State != Draft || got.Version != 0 || len(got.Audit) != 0 || len(genEvents(t, p, id)) != 0 {
		t.Errorf("a failed job insert left the campaign changed: %+v", got)
	}
}

func TestGenerateWorkerTimeout(t *testing.T) {
	w := NewGenerateWorker(&Generator{})
	if got := w.Timeout(&river.Job[jobs.GenerateArgs]{}); got != jobs.GenerateTimeout {
		t.Errorf("timeout %v, want %v", got, jobs.GenerateTimeout)
	}
	if MaxValidationAttempts != 3 || MaxTransportRetries != 3 {
		t.Errorf("attempt limits %d and %d, want 3 and 3", MaxValidationAttempts, MaxTransportRetries)
	}
}
