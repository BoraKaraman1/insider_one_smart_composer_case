package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// startServe runs serveOn with handler on a free port and returns its URL
// and the channel serveOn's result arrives on.
func startServe(t *testing.T, ctx context.Context, handler http.Handler) (string, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serveOn(ctx, quiet, &http.Server{Handler: handler}, ln, "test listening") }()
	return "http://" + ln.Addr().String(), done
}

// F8: serve returns only after the active request finished.
func TestServeWaitsForActiveRequests(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	url, done := startServe(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))

	status := make(chan int, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			status <- 0
			return
		}
		resp.Body.Close()
		status <- resp.StatusCode
	}()
	<-entered
	stop()
	select {
	case err := <-done:
		t.Fatalf("serve returned (%v) while a handler was active", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if got := <-status; got != http.StatusNoContent {
		t.Errorf("the drained request got %d, want 204", got)
	}
}

// A handler that never ends is cut after shutdownTimeout, and serve
// returns.
func TestServeClosesAfterShutdownTimeout(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 100 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = old })
	entered := make(chan struct{})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	url, done := startServe(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done() // ends when Close drops the connection
	}))
	go func() {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the shutdown timeout")
	}
}
