package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockprovider"
)

// mockproviderCmd serves the fake provider on its own database
// (MOCKPROVIDER_DATABASE_URL), never the app's, so it cannot read messages.
// It creates its tables at start. Chaos comes from MOCKPROVIDER_* variables.
func mockproviderCmd(ctx context.Context, log *slog.Logger, getenv func(string) string) error {
	srv, cleanup, err := newMockprovider(ctx, getenv)
	if err != nil {
		return err
	}
	defer cleanup()
	addr := getenv("MOCKPROVIDER_ADDR")
	if addr == "" {
		addr = ":8091"
	}
	hs := &http.Server{Addr: addr, Handler: srv, ReadHeaderTimeout: 5 * time.Second}
	return serve(ctx, log, hs, "mockprovider listening")
}

func newMockprovider(ctx context.Context, getenv func(string) string) (*mockprovider.Server, func(), error) {
	cfg, err := mockprovider.ConfigFromEnv(getenv)
	if err != nil {
		return nil, nil, err
	}
	url := getenv("MOCKPROVIDER_DATABASE_URL")
	if url == "" {
		return nil, nil, errors.New("MOCKPROVIDER_DATABASE_URL is not set (the provider's own database, not the app's)")
	}
	if url == getenv("DATABASE_URL") {
		return nil, nil, errors.New("MOCKPROVIDER_DATABASE_URL must not be the app database")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, nil, err
	}
	if err := mockprovider.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}
	return mockprovider.New(pool, cfg), pool.Close, nil
}
