package mockprovider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockprovider"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func newServer(t *testing.T, cfg mockprovider.Config) *httptest.Server {
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
	srv := httptest.NewServer(mockprovider.New(p, cfg))
	t.Cleanup(srv.Close)
	return srv
}

const (
	smsBody   = `{"channel":"sms","to":"+90 000 000 0001","body":"Merhaba Ayşe"}`
	emailBody = `{"channel":"email","to":"user1@example.com","subject":"Selam","body":"Merhaba Ayşe"}`
)

type reply struct {
	Status int
	ID     string
	Header http.Header
	Err    error
}

func post(t *testing.T, srv *httptest.Server, client *http.Client, key, chaos, body string) reply {
	t.Helper()
	req, err := http.NewRequest("POST", srv.URL+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if chaos != "" {
		req.Header.Set("X-Chaos", chaos)
	}
	if client == nil {
		client = srv.Client()
	}
	resp, err := client.Do(req)
	if err != nil {
		return reply{Err: err}
	}
	defer resp.Body.Close()
	var out struct {
		ID string `json:"id"`
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return reply{Status: resp.StatusCode, Header: resp.Header, Err: err}
	}
	_ = json.Unmarshal(data, &out)
	return reply{Status: resp.StatusCode, ID: out.ID, Header: resp.Header}
}

func stats(t *testing.T, srv *httptest.Server) mockprovider.Stats {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/v1/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/stats: %d", resp.StatusCode)
	}
	var s mockprovider.Stats
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func chaosOn() mockprovider.Config { return mockprovider.Config{ChaosHeader: true} }

func shortClient(srv *httptest.Server) *http.Client {
	c := srv.Client()
	c.Timeout = 300 * time.Millisecond
	return c
}

func TestMigrateTwiceIsANoop(t *testing.T) {
	ctx := context.Background()
	p, err := pgxpool.New(ctx, testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for i := range 2 {
		if err := mockprovider.Migrate(ctx, p); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}

func TestHealthz(t *testing.T) {
	srv := newServer(t, mockprovider.Config{})
	resp, err := srv.Client().Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: %d", resp.StatusCode)
	}
}

func TestSendAndReplay(t *testing.T) {
	srv := newServer(t, mockprovider.Config{})
	first := post(t, srv, nil, "m-1", "", smsBody)
	if first.Err != nil || first.Status != http.StatusAccepted || first.ID == "" {
		t.Fatalf("first send: %+v", first)
	}
	again := post(t, srv, nil, "m-1", "", smsBody)
	if again.Status != http.StatusAccepted || again.ID != first.ID {
		t.Errorf("replay: %+v; want 202 with id %q", again, first.ID)
	}
	other := post(t, srv, nil, "m-2", "", emailBody)
	if other.Status != http.StatusAccepted || other.ID == "" || other.ID == first.ID {
		t.Errorf("second key: %+v", other)
	}
	s := stats(t, srv)
	if s.Requests != 3 || s.UniqueSends != 2 || s.Replays != 1 || s.MaxSendsPerKey != 1 {
		t.Errorf("stats %+v; want 3 requests, 2 unique sends, 1 replay, max 1 per key", s)
	}
	if s.Outcomes["sent"] != 2 || s.Outcomes["replay"] != 1 {
		t.Errorf("outcomes %v", s.Outcomes)
	}
}

func TestRequestErrors(t *testing.T) {
	srv := newServer(t, mockprovider.Config{})
	cases := []struct {
		name, key, body string
		want            int
	}{
		{"no idempotency key", "", smsBody, http.StatusBadRequest},
		{"not JSON", "k-1", `{`, http.StatusBadRequest},
		{"unknown channel", "k-2", `{"channel":"fax","to":"x","body":"y"}`, http.StatusBadRequest},
		{"no recipient", "k-3", `{"channel":"sms","body":"y"}`, http.StatusBadRequest},
		{"no body", "k-4", `{"channel":"sms","to":"x"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if got := post(t, srv, nil, c.key, "", c.body); got.Status != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got.Status, c.want)
		}
	}
	if s := stats(t, srv); s.UniqueSends != 0 {
		t.Errorf("a rejected request was sent: %+v", s)
	}
}

func TestSameKeyDifferentPayload(t *testing.T) {
	srv := newServer(t, mockprovider.Config{})
	if r := post(t, srv, nil, "m-1", "", smsBody); r.Status != http.StatusAccepted {
		t.Fatalf("first send: %+v", r)
	}
	changed := strings.Replace(smsBody, "Ayşe", "Fatma", 1)
	if r := post(t, srv, nil, "m-1", "", changed); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("same key, other payload: %d, want 422", r.Status)
	}
	if s := stats(t, srv); s.UniqueSends != 1 || s.MaxSendsPerKey != 1 || s.Replays != 0 {
		t.Errorf("stats %+v; want 1 send and no replay", s)
	}
}

func TestConcurrentSameKeySendsOnce(t *testing.T) {
	srv := newServer(t, mockprovider.Config{Latency: 20 * time.Millisecond, Seed: 1})
	const n = 20
	replies := make([]reply, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			replies[i] = post(t, srv, nil, "same-key", "", smsBody)
		})
	}
	close(start)
	wg.Wait()
	for i, r := range replies {
		if r.Err != nil || r.Status != http.StatusAccepted || r.ID != replies[0].ID {
			t.Errorf("request %d: %+v; want 202 with id %q", i, r, replies[0].ID)
		}
	}
	s := stats(t, srv)
	if s.UniqueSends != 1 || s.MaxSendsPerKey != 1 || s.Replays != n-1 {
		t.Errorf("stats %+v; want 1 send, max 1 per key, %d replays", s, n-1)
	}
}

func TestErrorModesStoreNothing(t *testing.T) {
	cfg := chaosOn()
	cfg.RetryAfter = 2 * time.Second
	srv := newServer(t, cfg)
	cases := []struct {
		chaos      string
		status     int
		retryAfter string
	}{
		{"500", 500, ""},
		{"503", 503, ""},
		{"429", 429, "2"},
		{"429-no-retry-after", 429, ""},
		{"400", 400, ""},
	}
	for _, c := range cases {
		key := "err-" + c.chaos
		r := post(t, srv, nil, key, c.chaos, smsBody)
		if r.Err != nil || r.Status != c.status {
			t.Errorf("%s: %+v; want status %d", c.chaos, r, c.status)
			continue
		}
		if got := r.Header.Get("Retry-After"); got != c.retryAfter {
			t.Errorf("%s: Retry-After %q, want %q", c.chaos, got, c.retryAfter)
		}
		if ok := post(t, srv, nil, key, "ok", smsBody); ok.Status != http.StatusAccepted {
			t.Errorf("%s then ok: %+v", c.chaos, ok)
		}
	}
	s := stats(t, srv)
	if s.UniqueSends != len(cases) || s.Replays != 0 || s.MaxSendsPerKey != 1 {
		t.Errorf("stats %+v; want %d fresh sends and no replay", s, len(cases))
	}
	for _, c := range cases {
		if s.Outcomes[c.chaos] != 1 {
			t.Errorf("outcome %q counted %d times, want 1 (outcomes %v)", c.chaos, s.Outcomes[c.chaos], s.Outcomes)
		}
	}
}

func TestHangSendsNothing(t *testing.T) {
	srv := newServer(t, chaosOn())
	r := post(t, srv, shortClient(srv), "m-1", "hang", smsBody)
	var ne interface{ Timeout() bool }
	if r.Err == nil || !errors.As(r.Err, &ne) || !ne.Timeout() {
		t.Fatalf("hang: %+v; want a client timeout", r)
	}
	if s := stats(t, srv); s.UniqueSends != 0 {
		t.Errorf("hang sent: %+v", s)
	}
	if ok := post(t, srv, nil, "m-1", "ok", smsBody); ok.Status != http.StatusAccepted {
		t.Errorf("retry after hang: %+v", ok)
	}
	if s := stats(t, srv); s.UniqueSends != 1 || s.Replays != 0 {
		t.Errorf("retry after hang: %+v; want a fresh send", s)
	}
}

func TestDropRecordsTheSend(t *testing.T) {
	srv := newServer(t, chaosOn())
	r := post(t, srv, nil, "m-1", "drop", smsBody)
	if r.Err == nil {
		t.Fatalf("drop: got an answer %+v; want a broken connection", r)
	}
	if s := stats(t, srv); s.UniqueSends != 1 {
		t.Fatalf("drop did not record the send: %+v", s)
	}
	again := post(t, srv, nil, "m-1", "ok", smsBody)
	if again.Status != http.StatusAccepted || again.ID == "" {
		t.Fatalf("retry: %+v", again)
	}
	s := stats(t, srv)
	if s.UniqueSends != 1 || s.Replays != 1 || s.MaxSendsPerKey != 1 || s.Outcomes["drop"] != 1 {
		t.Errorf("stats %+v; want 1 send, 1 replay, 1 drop", s)
	}
}

func TestLateAnswerRecordsTheSend(t *testing.T) {
	srv := newServer(t, chaosOn())
	r := post(t, srv, shortClient(srv), "m-1", "late", smsBody)
	if r.Err == nil {
		t.Fatalf("late: got an answer in time %+v", r)
	}
	if again := post(t, srv, nil, "m-1", "ok", smsBody); again.Status != http.StatusAccepted {
		t.Fatalf("retry: %+v", again)
	}
	s := stats(t, srv)
	if s.UniqueSends != 1 || s.Replays != 1 || s.MaxSendsPerKey != 1 {
		t.Errorf("stats %+v; want 1 send and 1 replay", s)
	}
}

func TestChaosHeaderIgnoredWhenOff(t *testing.T) {
	srv := newServer(t, mockprovider.Config{})
	if r := post(t, srv, nil, "m-1", "503", smsBody); r.Status != http.StatusAccepted {
		t.Errorf("X-Chaos honored while off: %+v", r)
	}
}

func TestUnknownChaosValue(t *testing.T) {
	srv := newServer(t, chaosOn())
	if r := post(t, srv, nil, "m-1", "explode", smsBody); r.Status != http.StatusBadRequest {
		t.Errorf("unknown X-Chaos: %+v; want 400", r)
	}
}

func TestSeededChaosIsDeterministic(t *testing.T) {
	cfg := mockprovider.Config{Seed: 42, Rate503: 0.3, Rate429: 0.2, RetryAfter: time.Second}
	run := func() []int {
		srv := newServer(t, cfg)
		var codes []int
		for i := range 200 {
			r := post(t, srv, nil, "seq-"+strconv.Itoa(i), "", smsBody)
			codes = append(codes, r.Status)
		}
		return codes
	}
	a, b := run(), run()
	if !slices.Equal(a, b) {
		t.Fatal("two runs with one seed gave different outcomes")
	}
	count := map[int]int{}
	for _, c := range a {
		count[c]++
	}
	if count[503] < 35 || count[503] > 85 || count[429] < 20 || count[429] > 60 {
		t.Errorf("outcome counts %v far from the configured rates", count)
	}
	if count[202]+count[503]+count[429] != 200 {
		t.Errorf("unexpected statuses: %v", count)
	}
}

func TestStatsReceiveTimes(t *testing.T) {
	srv := newServer(t, chaosOn())
	before := time.Now()
	post(t, srv, nil, "s-1", "", smsBody)
	post(t, srv, nil, "s-2", "503", smsBody)
	post(t, srv, nil, "e-1", "", emailBody)
	after := time.Now()
	s := stats(t, srv)
	if len(s.ReceivedAt["sms"]) != 2 || len(s.ReceivedAt["email"]) != 1 {
		t.Fatalf("received_at %v; want 2 sms and 1 email", s.ReceivedAt)
	}
	for ch, times := range s.ReceivedAt {
		for _, at := range times {
			if at.Before(before.Add(-time.Second)) || at.After(after.Add(time.Second)) {
				t.Errorf("%s receive time %v outside the test run", ch, at)
			}
		}
	}
	if s.Requests != 3 {
		t.Errorf("requests %d, want 3", s.Requests)
	}
}
