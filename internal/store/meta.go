package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Querier runs one query. *pgxpool.Pool, *pgx.Conn and pgx.Tx satisfy it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// SeededAt returns the seed's time anchor from seed_meta, or nil if the
// database was never seeded.
func SeededAt(ctx context.Context, q Querier) (*time.Time, error) {
	var t time.Time
	err := q.QueryRow(ctx, "SELECT as_of FROM seed_meta").Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t = t.UTC()
	return &t, nil
}
