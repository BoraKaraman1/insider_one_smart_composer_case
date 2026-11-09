// Command app is the single binary for every service. The first argument
// picks the subcommand.
//
//	app migrate                    apply database migrations
//	app seed [-users N] [-seed S] [-as-of now|RFC3339] [-reset]
//	                               load synthetic users and events
//
// Both need DATABASE_URL. More subcommands (api, worker, mockprovider) are
// added as they are built.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/seed"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: app <migrate|seed> [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "migrate":
		err = migrate(ctx)
	case "seed":
		err = seedCmd(ctx, log, os.Args[2:])
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
