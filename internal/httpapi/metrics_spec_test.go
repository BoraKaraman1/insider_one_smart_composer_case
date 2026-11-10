//go:build spec

package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics/metricstest"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

// Plan 3, 3.2 and 3.4: GET /metrics is served outside auth, and
// http_requests_total labels its route by the mux pattern, never by the
// path, so no id becomes a label value.
func TestMetricsEndpoint(t *testing.T) {
	url, meta := testdb.Seeded(t)
	p := pool(t, url)
	reg := metrics.New()
	h := handler(t, &Server{
		Config:  Config{AdminToken: token, CampaignURLHosts: []string{"shop.example"}},
		Pool:    p,
		Now:     func() time.Time { return meta.AsOf },
		Metrics: reg,
	})

	f := insertCampaign(t, p, "in_review", 1)
	if rec := do(h, "GET", "/api/campaigns/"+f.ID, "", bearer); rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/campaigns/"+unknown, "", bearer); rec.Code != http.StatusNotFound {
		t.Fatalf("get unknown: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/campaigns/"+f.ID, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}

	rec := do(h, "GET", "/metrics", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics without a token: %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("/metrics content type %q", ct)
	}
	body := rec.Body.String()
	for _, id := range []string{f.ID, unknown, f.Revision} {
		if strings.Contains(body, id) {
			t.Errorf("/metrics contains the id %s", id)
		}
	}
	s := metricstest.Parse(t, body)
	for _, v := range s.LabelValues() {
		if metricstest.IDShaped(v) {
			t.Errorf("label value %q looks like an id", v)
		}
	}
	route := "route=GET /api/campaigns/{id}"
	for _, w := range []struct {
		status string
		n      float64
	}{{"200", 1}, {"404", 1}, {"401", 1}} {
		if got := s.Get("http_requests_total", route, "status="+w.status); got != w.n {
			t.Errorf("http_requests_total{%s,status=%s} = %v, want %v", route, w.status, got, w.n)
		}
	}
	// An unmatched path is one label value, not one per path.
	do(h, "GET", "/no/such/"+f.ID, "", bearer)
	do(h, "GET", "/no/such/other", "", bearer)
	s = metricstest.Scrape(t, h)
	for k := range s {
		if strings.HasPrefix(k, "http_requests_total") && strings.Contains(k, "/no/such") {
			t.Errorf("unmatched path became a label: %s", k)
		}
	}
}

// sse_streams counts open event streams and goes back down when one ends.
func TestMetricsSSEStreams(t *testing.T) {
	url, meta := testdb.Seeded(t)
	p := pool(t, url)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	reg := metrics.New()
	srv := httptest.NewServer(handler(t, &Server{
		Config:    Config{AdminToken: token, CampaignURLHosts: []string{"shop.example"}},
		Pool:      p,
		Now:       func() time.Time { return meta.AsOf },
		Hub:       NewHub(ctx, p, slog.New(slog.DiscardHandler)),
		Heartbeat: time.Second,
		Metrics:   reg,
	}))
	t.Cleanup(srv.Close)
	id := eventCampaign(t, p)
	_, frames := openStream(t, srv, "/api/campaigns/"+id+"/events", bearer)
	addGenEvent(t, p, id, "started", map[string]any{})
	sseNext(t, frames, 4*time.Second)
	if n := metricstest.Scrape(t, reg.Handler()).Get("sse_streams"); n != 1 {
		t.Errorf("one open stream: sse_streams = %v", n)
	}
	addGenEvent(t, p, id, "done", map[string]any{})
	sseEnded(t, frames, 4*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for metricstest.Scrape(t, reg.Handler()).Get("sse_streams") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("sse_streams did not go back to 0 after the stream ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
