package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics/metricstest"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func TestHealthcheckExitCodes(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(ok.Close)
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(unavailable.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, ok.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(hang.Close)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := "http://" + ln.Addr().String() + "/readyz"
	ln.Close()

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"200", []string{ok.URL + "/readyz"}, 0},
		{"503", []string{unavailable.URL + "/readyz"}, 1},
		{"refused", []string{refused}, 1},
		{"redirect is not 200", []string{redirect.URL}, 1},
		{"no url", nil, 2},
		{"two urls", []string{ok.URL, ok.URL}, 2},
	}
	for _, c := range cases {
		if got := healthcheckCmd(c.args); got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
	}

	start := time.Now()
	if got := healthcheckCmd([]string{hang.URL}); got != 1 {
		t.Errorf("hanging server: exit %d, want 1", got)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("hanging server: took %v, want the 2 s timeout", d)
	}
}

// İşçi durunca hazır ucu artık iki yüz dönmez.
func TestWorkerHealthAndMetrics(t *testing.T) {
	url, _ := testdb.Seeded(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	base := "http://" + addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- workerCmd(ctx, quiet, env(map[string]string{"LLM_MODE": "mock", "DATABASE_URL": url,
			"PROVIDER_URL": "http://127.0.0.1:9", "WORKER_METRICS_ADDR": addr}))
	}()

	get := func(path string) (int, string) {
		c := &http.Client{Timeout: time.Second}
		resp, err := c.Get(base + path)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("%s within 15s", what)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	waitFor("/readyz 200", func() bool { code, _ := get("/readyz"); return code == http.StatusOK })
	if code, _ := get("/healthz"); code != http.StatusOK {
		t.Errorf("/healthz: %d", code)
	}
	code, body := get("/metrics")
	if code != http.StatusOK {
		t.Fatalf("/metrics: %d", code)
	}
	s := metricstest.Parse(t, body)
	if !s.Has("worker_last_processed_timestamp_seconds") {
		t.Error("worker /metrics has no worker_last_processed_timestamp_seconds")
	}
	for _, name := range s.Names() {
		if strings.HasPrefix(name, "http_requests_total") || name == "sse_streams" {
			t.Errorf("worker exposes the api series %s", name)
		}
	}

	cancel()
	waitFor("/readyz to stop being 200 after stop", func() bool { code, _ := get("/readyz"); return code != http.StatusOK })
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("worker stopped with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Error("worker did not stop")
	}
}
