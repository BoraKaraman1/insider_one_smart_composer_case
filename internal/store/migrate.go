// Package store owns all SQL that is not compiled from a segment rule, and
// the migration runner.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/migrations"
)

// riverLock serializes River migrations across concurrent runs, like the
// goose session lock does for the app migrations.
const riverLock int64 = 0x717e7

// newMigrator builds a goose provider over the embedded migrations. A
// Postgres advisory lock makes concurrent runs (compose setup, tests) wait
// for each other instead of racing.
func newMigrator(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithSessionLocker(locker))
}

// newRiverMigrator builds a River migrator. The driver has no pool, so only
// the Tx variants work; callers pass a transaction on db's connection.
func newRiverMigrator() (*rivermigrate.Migrator[pgx.Tx], error) {
	return rivermigrate.New(riverpgxv5.New(nil), &rivermigrate.Config{Logger: slog.New(slog.DiscardHandler)})
}

// inTx runs fn in a transaction on one pgx connection taken from db, and
// commits if fn succeeds.
func inTx(ctx context.Context, db *sql.DB, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		tx, err := dc.(*stdlib.Conn).Conn().BeginTx(ctx, opts)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
}

// Migrate applies every pending migration: the app's (goose), then River's
// queue tables (rivermigrate). It does not close db.
func Migrate(ctx context.Context, db *sql.DB) error {
	p, err := newMigrator(db)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	rm, err := newRiverMigrator()
	if err != nil {
		return fmt.Errorf("migrate river: %w", err)
	}
	err = inTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", riverLock); err != nil {
			return err
		}
		//lint:ignore SA1019 the advisory lock in this transaction has to cover the migration
		_, err := rm.MigrateTx(ctx, tx, rivermigrate.DirectionUp, nil)
		return err
	})
	if err != nil {
		return fmt.Errorf("migrate river: %w", err)
	}
	return nil
}

// Pending reports whether db lacks any embedded migration or any River
// migration (for /readyz). It only reads, so it takes no lock.
func Pending(ctx context.Context, db *sql.DB) (bool, error) {
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return false, err
	}
	pending, err := p.HasPending(ctx)
	if err != nil || pending {
		return pending, err
	}
	rm, err := newRiverMigrator()
	if err != nil {
		return false, err
	}
	var ok bool
	err = inTx(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		res, err := rm.ValidateTx(ctx, tx, nil)
		if err != nil {
			return err
		}
		ok = res.OK
		return nil
	})
	return !ok, err
}
