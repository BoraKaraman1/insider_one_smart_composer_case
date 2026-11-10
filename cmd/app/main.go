// Command app is the single binary for every service. The first argument
// picks the subcommand.
//
//	app api                        serve the HTTP API (ADMIN_TOKEN, LISTEN_ADDR)
//	app worker                     run the River workers (LLM_MODE, see internal/llm;
//	                               PROVIDER_URL)
//	app migrate                    apply database migrations
//	app seed [-users N] [-seed S] [-as-of now|RFC3339] [-reset]
//	                               load synthetic users and events
//	app mockllm                    serve the fake Messages API (MOCKLLM_ADDR,
//	                               MOCKLLM_INVALID_FIRST); needs no database
//	app mockprovider               serve the fake SMS and email provider on its own
//	                               database (MOCKPROVIDER_DATABASE_URL, MOCKPROVIDER_*)
//	app eval [-cases path] [-out path] [-runs N] [-template name]
//	                               score the golden briefs (EVAL_ADMIN_URL, LLM_MODE)
//	app healthcheck <url>          one GET, exit 0 on 200; for compose healthchecks
//
// api, worker, migrate, seed and eval need a database URL. healthcheck,
// mockllm and mockprovider do not use the app database.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/httpapi"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/seed"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

func main() {
	// Before the signal handler: a healthcheck is one request and an exit code.
	if len(os.Args) >= 2 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheckCmd(os.Args[2:]))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: app <api|worker|migrate|seed|mockllm|mockprovider|eval|healthcheck> [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "api":
		err = apiCmd(ctx, log)
	case "worker":
		err = workerCmd(ctx, log, os.Getenv)
	case "mockllm":
		err = mockllmCmd(ctx, log, os.Getenv)
	case "mockprovider":
		err = mockproviderCmd(ctx, log, os.Getenv)
	case "migrate":
		err = migrate(ctx)
	case "seed":
		err = seedCmd(ctx, log, os.Args[2:])
	case "eval":
		err = evalCmd(ctx, log, os.Getenv, os.Args[2:])
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		log.Error("failed", "subcommand", os.Args[1], "error", err)
		os.Exit(1)
	}
}

func databaseURL() (string, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return "", errors.New("DATABASE_URL is not set")
	}
	return url, nil
}

func apiCmd(ctx context.Context, log *slog.Logger) error {
	cfg, err := httpapi.ConfigFromEnv(os.Getenv)
	if err != nil {
		return err // fail closed: no token, no explicit opt-out, no API
	}
	if cfg.AdminToken == "" {
		log.Warn("ALLOW_UNAUTHENTICATED=true: the API accepts requests without a token; local development only")
	}
	url, err := databaseURL()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	schemas, err := schema.Load()
	if err != nil {
		return err
	}
	// Insert-only: the api enqueues and cancels jobs; the worker runs them.
	rc, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: log})
	if err != nil {
		return err
	}
	// Request counters and the open-stream gauge. Database gauges are scraped
	// from the worker, so there is one collector.
	reg := metrics.New()
	srv := &httpapi.Server{Config: cfg, Pool: pool, Schemas: schemas, Log: log, River: rc,
		Hub: httpapi.NewHub(ctx, pool, log), Metrics: reg}
	h, err := srv.Handler()
	if err != nil {
		return err
	}
	hs := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return serve(ctx, log, hs, "api listening", "auth_required", cfg.AdminToken != "")
}

func migrate(ctx context.Context) error {
	url, err := databaseURL()
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer db.Close()
	return store.Migrate(ctx, db)
}

func seedCmd(ctx context.Context, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	users := fs.Int("users", 5000, "number of users")
	seedValue := fs.Int64("seed", 42, "random seed")
	asOfFlag := fs.String("as-of", "now", `time anchor: "now" or an RFC 3339 time`)
	reset := fs.Bool("reset", false, "delete users, events and all campaigns first, then reload")
	if err := fs.Parse(args); err != nil {
		return err
	}
	asOf := time.Now()
	if *asOfFlag != "now" {
		var err error
		if asOf, err = time.Parse(time.RFC3339Nano, *asOfFlag); err != nil {
			return fmt.Errorf("-as-of: %w", err)
		}
	}
	ds, err := seed.Generate(*seedValue, asOf, *users)
	if err != nil {
		return err
	}
	url, err := databaseURL()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	loaded, err := seed.Load(ctx, pool, ds, *reset)
	if err != nil {
		return err
	}
	if !loaded {
		log.Info("seed skipped: users already present (use -reset to reload)")
		return nil
	}
	log.Info("seed loaded", "users", len(ds.Users), "events", len(ds.Events), "seed", ds.Seed, "as_of", ds.AsOf)
	return nil
}
