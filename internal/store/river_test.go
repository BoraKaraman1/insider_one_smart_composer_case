package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

// The tables rivermigrate creates. Jobs cannot be inserted without them.
var riverTables = []string{"river_job", "river_leader", "river_queue", "river_migration"}

func missingRiverTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var missing []string
	for _, tbl := range riverTables {
		var ok bool
		if err := db.QueryRow("SELECT to_regclass('public.' || $1) IS NOT NULL", tbl).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if !ok {
			missing = append(missing, tbl)
		}
	}
	return missing
}

// TestMigrateRunsRiver checks that Migrate creates the River tables after
// the goose migrations, and that a second run is a no-op.
func TestMigrateRunsRiver(t *testing.T) {
	ctx := context.Background()
	db := freshDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if missing := missingRiverTables(t, db); len(missing) != 0 {
		t.Fatalf("after Migrate: River tables missing: %v", missing)
	}
	if pending, err := Pending(ctx, db); err != nil || pending {
		t.Fatalf("after Migrate: Pending = %v, %v; want false", pending, err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second run is not a no-op: %v", err)
	}
}

// TestPendingCoversRiver applies only the goose migrations. The database
// then has every app table but no River tables, so it is not ready: /readyz
// must not report ok for a database the worker cannot use.
func TestPendingCoversRiver(t *testing.T) {
	ctx := context.Background()
	db := freshDB(t)
	p, err := newMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if missing := missingRiverTables(t, db); len(missing) != len(riverTables) {
		t.Fatalf("goose alone created River tables; this test no longer isolates them (missing %v)", missing)
	}
	if pending, err := Pending(ctx, db); err != nil || !pending {
		t.Fatalf("goose done, River missing: Pending = %v, %v; want true", pending, err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate on a goose-only database: %v", err)
	}
	if pending, err := Pending(ctx, db); err != nil || pending {
		t.Fatalf("after Migrate: Pending = %v, %v; want false", pending, err)
	}
}

// TestTemplateHasRiver checks the template that make test builds, so every
// integration package gets River tables in its clone.
func TestTemplateHasRiver(t *testing.T) {
	url, _ := testdb.Seeded(t)
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if missing := missingRiverTables(t, db); len(missing) != 0 {
		t.Fatalf("template clone lacks River tables: %v", missing)
	}
	if pending, err := Pending(context.Background(), db); err != nil || pending {
		t.Fatalf("template clone: Pending = %v, %v; want false", pending, err)
	}
}
