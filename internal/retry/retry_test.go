package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestClassNames(t *testing.T) {
	names := map[Class]string{
		Success:     "success",
		Timeout:     "timeout",
		RateLimited: "rate_limited",
		ServerError: "server_error",
		ConnRefused: "conn_refused",
		ConnReset:   "conn_reset",
		ClientError: "client_error",
	}
	for class, want := range names {
		if string(class) != want {
			t.Errorf("class %q, want %q", class, want)
		}
	}
}

func TestRetryable(t *testing.T) {
	for _, c := range []Class{Timeout, RateLimited, ServerError, ConnRefused, ConnReset} {
		if !c.Retryable() {
			t.Errorf("%s is not retryable", c)
		}
	}
	for _, c := range []Class{Success, ClientError} {
		if c.Retryable() {
			t.Errorf("%s is retryable", c)
		}
	}
}

func statusServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.URL.Query().Get("retry_after"); v != "" {
			w.Header().Set("Retry-After", v)
		}
		var status int
		fmt.Sscan(r.URL.Query().Get("status"), &status)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, client *http.Client, url string) (*http.Response, error) {
	t.Helper()
	resp, err := client.Post(url, "application/json", strings.NewReader("{}"))
	if resp != nil {
		t.Cleanup(func() { resp.Body.Close() })
	}
	return resp, err
}

func TestClassifyStatus(t *testing.T) {
	srv := statusServer(t)
	cases := map[int]Class{
		200: Success, 201: Success, 202: Success, 204: Success,
		408: Timeout,
		429: RateLimited,
		500: ServerError, 502: ServerError, 503: ServerError, 504: ServerError, 529: ServerError,
		400: ClientError, 401: ClientError, 403: ClientError, 404: ClientError, 409: ClientError, 413: ClientError, 422: ClientError,
	}
	for status, want := range cases {
		resp, err := post(t, srv.Client(), fmt.Sprintf("%s/?status=%d", srv.URL, status))
		if err != nil {
			t.Fatalf("%d: %v", status, err)
		}
		got := Classify(resp, nil, now)
		if got.Class != want {
			t.Errorf("%d: class %s, want %s", status, got.Class, want)
		}
		if got.RetryAfter != 0 {
			t.Errorf("%d without Retry-After: RetryAfter %v, want 0", status, got.RetryAfter)
		}
	}
}

func TestClassifyRetryAfter(t *testing.T) {
	srv := statusServer(t)
	httpDate := func(d time.Duration) string { return now.Add(d).Format(http.TimeFormat) }
	cases := []struct {
		name, header string
		want         time.Duration
	}{
		{"seconds", "2", 2 * time.Second},
		{"zero seconds", "0", 0},
		{"seconds at the cap", "60", 60 * time.Second},
		{"seconds over the cap", "3600", 60 * time.Second},
		{"huge number", "99999999999999999999", 60 * time.Second},
		{"negative seconds", "-5", 0},
		{"HTTP date", httpDate(30 * time.Second), 30 * time.Second},
		{"HTTP date over the cap", httpDate(time.Hour), 60 * time.Second},
		{"HTTP date in the past", httpDate(-time.Hour), 0},
		{"garbage", "soon", 0},
	}
	for _, c := range cases {
		resp, err := post(t, srv.Client(), srv.URL+"/?status=429&retry_after="+url.QueryEscape(c.header))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := resp.Header.Get("Retry-After"); got != c.header {
			t.Fatalf("%s: server sent Retry-After %q, want %q", c.name, got, c.header)
		}
		got := Classify(resp, nil, now)
		if got.Class != RateLimited || got.RetryAfter != c.want {
			t.Errorf("%s (%q): %s, %v; want rate_limited, %v", c.name, c.header, got.Class, got.RetryAfter, c.want)
		}
	}
}

func freshClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true}}
}

func hijackServer(t *testing.T, fn func(net.Conn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		fn(conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClassifyConnRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	resp, err := post(t, freshClient(2*time.Second), "http://"+addr)
	if err == nil {
		t.Fatal("dialing a closed port succeeded")
	}
	if got := Classify(resp, err, now); got.Class != ConnRefused {
		t.Errorf("closed port: %s (%v), want conn_refused", got.Class, err)
	}
}

func TestClassifyConnClosedBeforeResponse(t *testing.T) {
	srv := hijackServer(t, func(c net.Conn) { c.Close() })
	resp, err := post(t, freshClient(2*time.Second), srv.URL)
	if err == nil {
		t.Fatal("request succeeded on a closed connection")
	}
	if got := Classify(resp, err, now); got.Class != ConnReset {
		t.Errorf("closed before the response: %s (%v), want conn_reset", got.Class, err)
	}
}

func TestClassifyConnResetByPeer(t *testing.T) {
	srv := hijackServer(t, func(c net.Conn) {
		c.(*net.TCPConn).SetLinger(0)
		c.Close()
	})
	resp, err := post(t, freshClient(2*time.Second), srv.URL)
	if err == nil {
		t.Fatal("request succeeded on a reset connection")
	}
	if got := Classify(resp, err, now); got.Class != ConnReset {
		t.Errorf("reset by peer: %s (%v), want conn_reset", got.Class, err)
	}
}

func TestClassifyUnexpectedEOFInBody(t *testing.T) {
	srv := hijackServer(t, func(c net.Conn) {
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort")
		c.Close()
	})
	resp, err := post(t, freshClient(2*time.Second), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(resp.Body)
	if !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatalf("read error %v, want unexpected EOF", readErr)
	}
	if got := Classify(nil, readErr, now); got.Class != ConnReset {
		t.Errorf("truncated body: %s, want conn_reset", got.Class)
	}
}

func slowServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // sunucu kapanmayı gövde okununca fark eder
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClassifyClientTimeout(t *testing.T) {
	srv := slowServer(t)
	resp, err := post(t, freshClient(50*time.Millisecond), srv.URL)
	if err == nil {
		t.Fatal("request succeeded past the client timeout")
	}
	if got := Classify(resp, err, now); got.Class != Timeout {
		t.Errorf("client timeout: %s (%v), want timeout", got.Class, err)
	}
}

func TestClassifyContextDeadline(t *testing.T) {
	srv := slowServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", srv.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := freshClient(0).Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("request succeeded past the context deadline")
	}
	if got := Classify(nil, err, now); got.Class != Timeout {
		t.Errorf("context deadline: %s (%v), want timeout", got.Class, err)
	}
}

func TestClassifyWrappedErrors(t *testing.T) {
	cases := []struct {
		err  error
		want Class
	}{
		{context.DeadlineExceeded, Timeout},
		{fmt.Errorf("send: %w", context.DeadlineExceeded), Timeout},
		{io.EOF, ConnReset},
		{fmt.Errorf("send: %w", io.EOF), ConnReset},
		{fmt.Errorf("read: %w", io.ErrUnexpectedEOF), ConnReset},
	}
	for _, c := range cases {
		if got := Classify(nil, c.err, now); got.Class != c.want {
			t.Errorf("%v: %s, want %s", c.err, got.Class, c.want)
		}
	}
}

func ceiling(attempt int) time.Duration {
	if attempt >= 6 {
		return 60 * time.Second
	}
	return time.Second << attempt
}

func TestBackoffFullJitter(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for attempt := 0; attempt <= 8; attempt++ {
		top := ceiling(attempt)
		const n = 2000
		lo, hi, sum := time.Duration(math.MaxInt64), time.Duration(0), time.Duration(0)
		for range n {
			d := Backoff(attempt, rng)
			if d < 0 || d > top {
				t.Fatalf("attempt %d: delay %v outside [0, %v]", attempt, d, top)
			}
			lo, hi, sum = min(lo, d), max(hi, d), sum+d
		}
		if lo > top/10 {
			t.Errorf("attempt %d: smallest of %d delays is %v; full jitter reaches near 0", attempt, n, lo)
		}
		if hi < top*9/10 {
			t.Errorf("attempt %d: largest of %d delays is %v; full jitter reaches near %v", attempt, n, hi, top)
		}
		if mean := sum / n; mean < top*4/10 || mean > top*6/10 {
			t.Errorf("attempt %d: mean %v, want about %v", attempt, mean, top/2)
		}
	}
}

func TestBackoffLargeAttemptsStayCapped(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, attempt := range []int{6, 30, 62, 63, 64, 65, 1000, math.MaxInt} {
		for range 200 {
			if d := Backoff(attempt, rng); d < 0 || d > 60*time.Second {
				t.Fatalf("attempt %d: delay %v outside [0, 60s] (shift overflow?)", attempt, d)
			}
		}
	}
}

func TestBackoffIsDeterministic(t *testing.T) {
	a, b := rand.New(rand.NewPCG(7, 7)), rand.New(rand.NewPCG(7, 7))
	for attempt := range 10 {
		if x, y := Backoff(attempt, a), Backoff(attempt, b); x != y {
			t.Fatalf("attempt %d: %v and %v from the same seed", attempt, x, y)
		}
	}
}
