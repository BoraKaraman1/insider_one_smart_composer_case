package seed

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func migratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	url := testdb.Fresh(t)
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func count(t *testing.T, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLoad(t *testing.T) {
	ctx := context.Background()
	pool := migratedPool(t)
	ds := mustGenerate(t, 42, time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC), 500)

	loaded, err := Load(ctx, pool, ds, false)
	if err != nil || !loaded {
		t.Fatalf("first load: loaded=%v err=%v", loaded, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM users"); n != 500 {
		t.Fatalf("users: %d", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM user_events"); n != len(ds.Events) {
		t.Fatalf("events: %d, want %d", n, len(ds.Events))
	}

	var asOf time.Time
	var seedValue int64
	var userCount int
	if err := pool.QueryRow(ctx, "SELECT as_of, seed, user_count FROM seed_meta").Scan(&asOf, &seedValue, &userCount); err != nil {
		t.Fatal(err)
	}
	if !asOf.Equal(ds.AsOf) || seedValue != 42 || userCount != 500 {
		t.Fatalf("seed_meta: %v %d %d, want %v 42 500", asOf, seedValue, userCount, ds.AsOf)
	}

	var first string
	var signup time.Time
	if err := pool.QueryRow(ctx, "SELECT first_name, signup_at FROM users WHERE id = $1", ds.Users[0].ID).Scan(&first, &signup); err != nil {
		t.Fatal(err)
	}
	if first != EdgeLongName || !signup.Equal(ds.Users[0].SignupAt) {
		t.Fatalf("user 1 round trip: %q %v, want %q %v", first, signup, EdgeLongName, ds.Users[0].SignupAt)
	}
	if n := count(t, pool, "SELECT count(*) FROM users WHERE first_name = '{{first_name}}'"); n != 1 {
		t.Fatalf("template-looking name not stored literally: %d rows", n)
	}

	again := mustGenerate(t, 7, time.Now(), 300)
	if loaded, err := Load(ctx, pool, again, false); err != nil || loaded {
		t.Fatalf("second load without reset: loaded=%v err=%v", loaded, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM users"); n != 500 {
		t.Fatalf("users after skipped load: %d", n)
	}

	if _, err := pool.Exec(ctx, "INSERT INTO campaigns (brief) VALUES ('old campaign')"); err != nil {
		t.Fatal(err)
	}
	if loaded, err := Load(ctx, pool, again, true); err != nil || !loaded {
		t.Fatalf("reset load: loaded=%v err=%v", loaded, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM users"); n != 300 {
		t.Fatalf("users after reset: %d", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM campaigns"); n != 0 {
		t.Fatalf("campaigns after reset: %d", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM seed_meta WHERE seed = 7"); n != 1 {
		t.Fatal("seed_meta not replaced")
	}
}
