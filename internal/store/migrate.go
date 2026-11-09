// Package store owns all SQL that is not compiled from a segment rule, and
// the migration runner.
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/migrations"
)

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

// Migrate applies every pending migration. It does not close db.
func Migrate(ctx context.Context, db *sql.DB) error {
	p, err := newMigrator(db)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
