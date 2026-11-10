package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockllm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

const specBrief = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder."

func isEventsURL(raw, id string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Path == "/api/campaigns/"+id+"/events" && u.Host == ""
}

// eventsServer serves the API over real HTTP, since SSE needs streaming.
// The hub listens on its own connection until the test ends.
func eventsServer(t *testing.T, heartbeat time.Duration, writeTimeout time.Duration) (*httptest.Server, *pgxpool.Pool, *Server) {
	t.Helper()
	url, meta := testdb.Seeded(t)
	p := pool(t, url)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &Server{
		Config:    Config{AdminToken: token, CampaignURLHosts: []string{"shop.example"}},
		Pool:      p,
		Now:       func() time.Time { return meta.AsOf },
		Hub:       NewHub(ctx, p, slog.New(slog.DiscardHandler)),
		Heartbeat: heartbeat,
	}
	srv := httptest.NewUnstartedServer(handler(t, s))
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, p, s
}

type sseFrame struct {
	ID      int64
	Event   string
	Data    map[string]any
	Comment bool
}

// openStream starts a GET and delivers frames until the server ends the
// stream, which closes the channel.
func openStream(t *testing.T, srv *httptest.Server, path string, header map[string]string) (*http.Response, <-chan sseFrame) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	frames := make(chan sseFrame, 1024)
	go func() {
		defer close(frames)
		var f sseFrame
		started := false
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if started {
					frames <- f
				}
				f, started = sseFrame{}, false
			case strings.HasPrefix(line, ":"):
				f.Comment, started = true, true
			case strings.HasPrefix(line, "id: "):
				f.ID, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
				started = true
			case strings.HasPrefix(line, "event: "):
				f.Event, started = strings.TrimPrefix(line, "event: "), true
			case strings.HasPrefix(line, "data: "):
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f.Data)
				started = true
			}
		}
	}()
	return resp, frames
}

// next returns the next event frame, skipping heartbeats.
func sseNext(t *testing.T, frames <-chan sseFrame, within time.Duration) sseFrame {
	t.Helper()
	timeout := time.After(within)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatal("the stream ended early")
			}
			if !f.Comment {
				return f
			}
		case <-timeout:
			t.Fatalf("no event within %v", within)
		}
	}
}

// ended waits for the server to close the stream.
func sseEnded(t *testing.T, frames <-chan sseFrame, within time.Duration) {
	t.Helper()
	timeout := time.After(within)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return
			}
			if !f.Comment {
				t.Fatalf("event %+v after the stream should have ended", f)
			}
		case <-timeout:
			t.Fatalf("the stream did not end within %v", within)
		}
	}
}

func eventCampaign(t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	return insertCampaign(t, p, "generating", 1).ID
}

func addGenEvent(t *testing.T, p *pgxpool.Pool, id, typ string, payload map[string]any) int64 {
	t.Helper()
	n, err := store.InsertGenerationEvent(context.Background(), p, id, typ, payload)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The stream sends the stored backlog first, then live events, and ends
// after done.
func TestEventsBacklogThenLive(t *testing.T) {
	srv, p, _ := eventsServer(t, 15*time.Second, 0)
	id := eventCampaign(t, p)
	first := addGenEvent(t, p, id, "segment_ready", map[string]any{"description": "d"})
	second := addGenEvent(t, p, id, "delta", map[string]any{"field": "sms.body", "text": "Mer"})

	resp, frames := openStream(t, srv, "/api/campaigns/"+id+"/events", bearer)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(resp.Header.Get("Cache-Control"), "no-cache") {
		t.Fatalf("status %d, headers %v", resp.StatusCode, resp.Header)
	}
	if f := sseNext(t, frames, 3*time.Second); f.ID != first || f.Event != "segment_ready" || f.Data["description"] != "d" {
		t.Errorf("first frame %+v", f)
	}
	if f := sseNext(t, frames, 3*time.Second); f.ID != second || f.Event != "delta" || f.Data["text"] != "Mer" {
		t.Errorf("second frame %+v", f)
	}
	live := addGenEvent(t, p, id, "delta", map[string]any{"field": "sms.body", "text": "haba"})
	if f := sseNext(t, frames, 3*time.Second); f.ID != live || f.Data["text"] != "haba" {
		t.Errorf("live frame %+v", f)
	}
	done := addGenEvent(t, p, id, "done", map[string]any{"revision_id": "r"})
	if f := sseNext(t, frames, 3*time.Second); f.ID != done || f.Event != "done" {
		t.Errorf("done frame %+v", f)
	}
	sseEnded(t, frames, 3*time.Second)
}

func TestEventsEndAfterFailed(t *testing.T) {
	srv, p, _ := eventsServer(t, 15*time.Second, 0)
	id := eventCampaign(t, p)
	addGenEvent(t, p, id, "failed", map[string]any{"reason": "refusal"})
	_, frames := openStream(t, srv, "/api/campaigns/"+id+"/events", bearer)
	if f := sseNext(t, frames, 3*time.Second); f.Event != "failed" {
		t.Errorf("frame %+v", f)
	}
	sseEnded(t, frames, 3*time.Second)
}

// A reconnect resumes after the last id it saw, from the Last-Event-ID
// header (browsers and our fetch client) or ?after= (curl).
func TestEventsResume(t *testing.T) {
	srv, p, _ := eventsServer(t, 15*time.Second, 0)
	id := eventCampaign(t, p)
	ids := []int64{
		addGenEvent(t, p, id, "segment_ready", map[string]any{}),
		addGenEvent(t, p, id, "delta", map[string]any{"text": "a"}),
		addGenEvent(t, p, id, "delta", map[string]any{"text": "b"}),
		addGenEvent(t, p, id, "done", map[string]any{}),
	}
	path := "/api/campaigns/" + id + "/events"
	cases := []struct {
		name   string
		path   string
		header map[string]string
		want   []int64
	}{
		{"Last-Event-ID", path, map[string]string{"Authorization": bearer["Authorization"], "Last-Event-ID": strconv.FormatInt(ids[0], 10)}, ids[1:]},
		{"after", path + "?after=" + strconv.FormatInt(ids[1], 10), bearer, ids[2:]},
	}
	for _, c := range cases {
		_, frames := openStream(t, srv, c.path, c.header)
		var got []int64
		for range c.want {
			got = append(got, sseNext(t, frames, 3*time.Second).ID)
		}
		sseEnded(t, frames, 3*time.Second)
		if !equalIDs(got, c.want) {
			t.Errorf("%s: ids %v, want %v", c.name, got, c.want)
		}
	}
	for _, bad := range []string{"abc", "-1", "1.5"} {
		resp, _ := openStream(t, srv, path, map[string]string{"Authorization": bearer["Authorization"], "Last-Event-ID": bad})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Last-Event-ID %q: %d, want 400", bad, resp.StatusCode)
		}
		resp, _ = openStream(t, srv, path+"?after="+url.QueryEscape(bad), bearer)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("after=%q: %d, want 400", bad, resp.StatusCode)
		}
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The token travels only in the Authorization header: a query token would
// end up in logs and browser history.
func TestEventsAuth(t *testing.T) {
	srv, p, _ := eventsServer(t, 15*time.Second, 0)
	id := eventCampaign(t, p)
	path := "/api/campaigns/" + id + "/events"
	if resp, _ := openStream(t, srv, path, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", resp.StatusCode)
	}
	if resp, _ := openStream(t, srv, path+"?access_token="+token, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token in the query: %d, want 401", resp.StatusCode)
	}
	if resp, _ := openStream(t, srv, "/api/campaigns/"+unknown+"/events", bearer); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown campaign: %d, want 404", resp.StatusCode)
	}
}

// An idle stream gets heartbeat comments, so proxies keep it open.
func TestEventsHeartbeat(t *testing.T) {
	srv, p, _ := eventsServer(t, 50*time.Millisecond, 0)
	id := eventCampaign(t, p)
	_, frames := openStream(t, srv, "/api/campaigns/"+id+"/events", bearer)
	select {
	case f, ok := <-frames:
		if !ok || !f.Comment {
			t.Errorf("first frame %+v (open %v), want a heartbeat comment", f, ok)
		}
	case <-time.After(2 * time.Second):
		t.Error("no heartbeat within 2s")
	}
}

// The server's WriteTimeout would cut a long stream. The handler clears its
// write deadline, so the stream outlives it.
func TestEventsOutliveWriteTimeout(t *testing.T) {
	srv, p, _ := eventsServer(t, 50*time.Millisecond, 200*time.Millisecond)
	id := eventCampaign(t, p)
	_, frames := openStream(t, srv, "/api/campaigns/"+id+"/events", bearer)
	time.Sleep(700 * time.Millisecond)
	done := addGenEvent(t, p, id, "done", map[string]any{})
	if f := sseNext(t, frames, 3*time.Second); f.ID != done {
		t.Errorf("frame %+v, want the done event after the write timeout passed", f)
	}
}

// A missed notification is covered by the poll: an event inserted without
// pg_notify still arrives.
func TestEventsPollCoversMissedNotify(t *testing.T) {
	srv, p, _ := eventsServer(t, 15*time.Second, 0)
	id := eventCampaign(t, p)
	_, frames := openStream(t, srv, "/api/campaigns/"+id+"/events", bearer)
	time.Sleep(100 * time.Millisecond)
	var n int64
	if err := p.QueryRow(context.Background(), `INSERT INTO generation_events (campaign_id, type, payload)
		VALUES ($1, 'delta', '{"text":"silent"}') RETURNING id`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if f := sseNext(t, frames, 4*time.Second); f.ID != n || f.Data["text"] != "silent" {
		t.Errorf("frame %+v", f)
	}
}

// liveStack runs everything in the test: the API, a River worker with the
// generate worker, and the in-process mock LLM behind the real client.
func liveStack(t *testing.T) (*httptest.Server, *pgxpool.Pool, *mockllm.Server) {
	t.Helper()
	url, meta := testdb.Seeded(t)
	p := pool(t, url)
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := mockllm.New(mockllm.Options{})
	mockSrv := httptest.NewServer(m)
	t.Cleanup(mockSrv.Close)
	cl, err := llm.New(llm.Config{BaseURL: mockSrv.URL, APIKey: "test-placeholder", Schemas: set,
		StallTimeout: 2 * time.Second, TotalTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	gen := &campaign.Generator{
		Pool: p, Schemas: set, LLM: cl,
		Now:   func() time.Time { return meta.AsOf },
		Rand:  rand.New(rand.NewPCG(1, 2)),
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, campaign.NewGenerateWorker(gen))
	log := slog.New(slog.DiscardHandler)
	rc, err := river.NewClient(riverpgxv5.New(p), jobs.ClientConfig(workers, log))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := rc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = rc.Stop(stopCtx)
		cancel()
	})
	s := &Server{
		Config: Config{AdminToken: token, CampaignURLHosts: []string{"shop.example"}},
		Pool:   p, River: rc, Schemas: set,
		Now: func() time.Time { return meta.AsOf },
		Hub: NewHub(ctx, p, log), Heartbeat: time.Second,
	}
	srv := httptest.NewServer(handler(t, s))
	t.Cleanup(srv.Close)
	return srv, p, m
}

func apiCall(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", bearer["Authorization"])
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func startGenerate(t *testing.T, srv *httptest.Server, id string, version int) string {
	t.Helper()
	code, body := apiCall(t, srv, "POST", "/api/campaigns/"+id+"/generate", versionBody(version))
	u, _ := body["events_url"].(string)
	if code != http.StatusAccepted || !isEventsURL(u, id) {
		t.Fatalf("generate: %d %v", code, body)
	}
	return u
}

// collect reads one generation's stream to its end.
func sseCollect(t *testing.T, srv *httptest.Server, path string, header map[string]string) []sseFrame {
	t.Helper()
	_, frames := openStream(t, srv, path, header)
	var out []sseFrame
	timeout := time.After(60 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return out
			}
			if !f.Comment {
				out = append(out, f)
			}
		case <-timeout:
			t.Fatalf("the stream did not end; got %d events", len(out))
		}
	}
}

// TestGenerateEndToEnd: create, generate and watch the stream with a real
// worker. Events arrive in order, the deltas rebuild the stored copy, a
// reconnect gets only later events, and a regenerate streams only its own
// events.
func TestGenerateEndToEnd(t *testing.T) {
	srv, _, _ := liveStack(t)
	code, created := apiCall(t, srv, "POST", "/api/campaigns", createBody(specBrief, okURL))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, created)
	}
	id := created["campaign"].(map[string]any)["id"].(string)
	events := sseCollect(t, srv, startGenerate(t, srv, id, 0), bearer)

	var types []string
	for i, f := range events {
		if i > 0 && f.ID <= events[i-1].ID {
			t.Errorf("ids out of order: %d after %d", f.ID, events[i-1].ID)
		}
		if len(types) == 0 || f.Event != "delta" || types[len(types)-1] != "delta" {
			types = append(types, f.Event)
		}
	}
	if want := []string{"started", "segment_ready", "delta", "done"}; strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("event types %v, want %v", types, want)
	}

	code, view := apiCall(t, srv, "GET", "/api/campaigns/"+id, "")
	if code != http.StatusOK || view["campaign"].(map[string]any)["state"] != "in_review" {
		t.Fatalf("campaign after done: %d %v", code, view["campaign"])
	}
	rev := view["revision"].(map[string]any)
	rebuilt := map[string]string{}
	for _, f := range events {
		if f.Event == "delta" {
			field, _ := f.Data["field"].(string)
			text, _ := f.Data["text"].(string)
			rebuilt[field] += text
		}
	}
	if rebuilt["sms.body"] != rev["sms_body"] || rebuilt["email.subject"] != rev["email_subject"] || rebuilt["email.body"] != rev["email_body"] {
		t.Errorf("deltas rebuild %q; stored revision %v", rebuilt, rev)
	}

	// Reconnect after segment_ready: only the later events come back.
	path := "/api/campaigns/" + id + "/events"
	resumed := sseCollect(t, srv, path, map[string]string{"Authorization": bearer["Authorization"], "Last-Event-ID": strconv.FormatInt(events[1].ID, 10)})
	if len(resumed) != len(events)-2 || resumed[0].ID != events[2].ID || resumed[len(resumed)-1].Event != "done" {
		t.Errorf("resumed %d events from id %d, want the %d after segment_ready", len(resumed), resumed[0].ID, len(events)-2)
	}

	// Regenerate: the new stream starts with its own started event and does
	// not end on the first generation's done.
	version := int(view["campaign"].(map[string]any)["version"].(float64))
	again := sseCollect(t, srv, startGenerate(t, srv, id, version), bearer)
	if len(again) < 3 || again[0].Event != "started" || again[0].ID <= events[len(events)-1].ID || again[len(again)-1].Event != "done" {
		t.Errorf("regenerate stream %+v", again)
	}
}
