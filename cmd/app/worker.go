package main

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/delivery"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockllm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
)

// workerCmd runs the River client with every queue. It is the only process
// that calls Claude and the provider (PROVIDER_URL). It fails closed before
// touching the database: an unknown LLM_MODE, LLM_MODE=anthropic without a
// key, or no provider URL stops it. It serves /metrics, /healthz and
// /readyz on WORKER_METRICS_ADDR (default :9090).
func workerCmd(ctx context.Context, log *slog.Logger, getenv func(string) string) error {
	llmCfg, err := llm.ConfigFromEnv(getenv)
	if err != nil {
		return err
	}
	url := getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is not set")
	}
	providerURL := getenv("PROVIDER_URL")
	if providerURL == "" {
		return errors.New("PROVIDER_URL is not set (the mock provider, for example http://mockprovider:8091)")
	}
	schemas, err := schema.Load()
	if err != nil {
		return err
	}
	llmCfg.Schemas = schemas
	client, err := llm.New(llmCfg)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	reg := metrics.New()
	reg.WorkerStarted()
	reg.WatchDatabase(pool)

	gen := &campaign.Generator{
		Pool:          pool,
		Schemas:       schemas,
		LLM:           client,
		Now:           time.Now,
		Rand:          rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		Sleep:         sleep,
		DeltaInterval: campaign.DefaultDeltaInterval,
		Log:           log,
		Metrics:       reg,
	}
	fanout := &delivery.FanoutWorker{DB: pool, Now: time.Now}
	send := &delivery.SendWorker{
		DB:              pool,
		ProviderURL:     providerURL,
		Limiters:        delivery.NewLimiters(),
		ProviderTimeout: delivery.ProviderTimeout,
		Now:             time.Now,
		Backoff:         delivery.RandomBackoff(rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))),
		Client:          &http.Client{},
		Log:             log,
		Metrics:         reg,
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, campaign.NewGenerateWorker(gen))
	river.AddWorker(workers, fanout)
	river.AddWorker(workers, send)
	rc, err := river.NewClient(riverpgxv5.New(pool), jobs.ClientConfig(workers, log))
	if err != nil {
		return err
	}
	fanout.Jobs = rc

	addr := getenv("WORKER_METRICS_ADDR")
	if addr == "" {
		addr = ":9090"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	var ready atomic.Bool
	hs := &http.Server{Handler: workerMux(reg, pool, &ready), ReadHeaderTimeout: 5 * time.Second}
	// The listener outlives ctx: it answers /readyz with 503 while River
	// stops, and closes after.
	srvCtx, srvStop := context.WithCancel(context.WithoutCancel(ctx))
	defer srvStop()
	served := make(chan error, 1)
	go func() { served <- serveOn(srvCtx, log, hs, ln, "worker metrics listening") }()

	if err := rc.Start(ctx); err != nil {
		srvStop()
		<-served
		return err
	}
	ready.Store(true)
	// Never log the key; only whether it is there.
	log.Info("worker started", "llm_mode", getenv("LLM_MODE"), "model", client.Model(), "anthropic_key", keyState(getenv("LLM_MODE")))
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-served:
		log.Error("worker metrics listener failed; stopping", "error", serveErr)
	}
	ready.Store(false)
	stop, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = rc.Stop(stop)
	if serveErr != nil {
		return errors.Join(serveErr, err)
	}
	srvStop()
	return errors.Join(err, <-served)
}

// workerMux serves the worker's metrics and health. /healthz is liveness
// only. /readyz is 200 while River runs and the database answers a ping.
// Neither claims that jobs are being consumed: that is what
// queue_oldest_job_age_seconds and worker_last_processed_timestamp_seconds
// show.
func workerMux(reg *metrics.Registry, pool *pgxpool.Pool, ready *atomic.Bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", reg.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "river is not running", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func keyState(mode string) string {
	if mode == "anthropic" {
		return "present" // ConfigFromEnv refused to start without one
	}
	return "not used"
}

// sleep waits for d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// mockllmCmd serves the fake Messages API for LLM_MODE=mock. It needs no
// database and no key.
func mockllmCmd(ctx context.Context, log *slog.Logger, getenv func(string) string) error {
	addr := getenv("MOCKLLM_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	hs := &http.Server{Addr: addr, Handler: mockllmHandler(getenv), ReadHeaderTimeout: 5 * time.Second}
	return serve(ctx, log, hs, "mockllm listening")
}

func mockllmHandler(getenv func(string) string) *mockllm.Server {
	return mockllm.New(mockllm.Options{InvalidFirst: getenv("MOCKLLM_INVALID_FIRST") == "true"})
}

// shutdownTimeout is how long active requests get to finish on shutdown.
var shutdownTimeout = 10 * time.Second

// serve listens on hs.Addr and runs hs until ctx ends. attrs are logged
// with msg.
func serve(ctx context.Context, log *slog.Logger, hs *http.Server, msg string, attrs ...any) error {
	ln, err := net.Listen("tcp", hs.Addr)
	if err != nil {
		return err
	}
	return serveOn(ctx, log, hs, ln, msg, attrs...)
}

// serveOn runs hs on ln until ctx ends, then shuts it down. It returns only
// after the active requests finished, or after shutdownTimeout, when the
// remaining connections are closed. So the caller's deferred cleanup (the
// database pool) never runs under a live handler.
func serveOn(ctx context.Context, log *slog.Logger, hs *http.Server, ln net.Listener, msg string, attrs ...any) error {
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	log.Info(msg, append([]any{"addr", ln.Addr().String()}, attrs...)...)
	select {
	case err := <-errc:
		return err // Serve failed on its own; ctx is still live
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(drain); err != nil {
		// Long-lived streams (SSE) keep their connections open until here.
		log.Warn("shutdown timed out; closing the remaining connections", "timeout", shutdownTimeout.String(), "error", err)
		_ = hs.Close()
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
