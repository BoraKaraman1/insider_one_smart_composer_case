package seed

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// lockID serializes concurrent loaders (two setup runs at once).
const lockID int64 = 0x5eed

// Load writes ds in one transaction and reports whether it wrote anything.
//
// Without reset, existing data is kept: if any user exists, nothing is
// written, so running setup twice is safe. With reset, users, events,
// seed_meta and every campaign (with its revisions, audit events, LLM calls
// and messages) are removed first. That is a dev reset that re-anchors the
// data at ds.AsOf.
func Load(ctx context.Context, pool *pgxpool.Pool, ds Dataset, reset bool) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) // no-op after Commit

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		return false, err
	}
	if reset {
		if _, err := tx.Exec(ctx, "TRUNCATE users, seed_meta, campaigns CASCADE"); err != nil {
			return false, err
		}
	} else {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users)").Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
	}

	users := pgx.CopyFromSlice(len(ds.Users), func(i int) ([]any, error) {
		u := ds.Users[i]
		return []any{u.ID, u.FirstName, u.LastName, u.Email, u.Phone, u.City, u.Language,
			u.LoyaltyTier, u.SignupAt, u.ConsentSMS, u.ConsentEmail}, nil
	})
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"users"}, []string{"id", "first_name", "last_name", "email",
		"phone", "city", "language", "loyalty_tier", "signup_at", "consent_sms", "consent_email"}, users); err != nil {
		return false, fmt.Errorf("seed users: %w", err)
	}
	events := pgx.CopyFromSlice(len(ds.Events), func(i int) ([]any, error) {
		e := ds.Events[i]
		return []any{e.UserID, e.Type, e.OccurredAt}, nil
	})
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"user_events"}, []string{"user_id", "type", "occurred_at"}, events); err != nil {
		return false, fmt.Errorf("seed events: %w", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO seed_meta (as_of, seed, user_count) VALUES ($1, $2, $3)",
		ds.AsOf, ds.Seed, len(ds.Users)); err != nil {
		return false, err
	}
	// Collect planner statistics now. Without them, queries right after a
	// bulk load (before autovacuum gets to it) pick seq-scan plans that are
	// about 10x slower.
	if _, err := tx.Exec(ctx, "ANALYZE users, user_events"); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
