package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

// freshDB opens an empty, unmigrated database for one test.
func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var appTables = []string{"users", "user_events", "seed_meta", "campaigns", "campaign_revisions", "campaign_events", "llm_calls", "messages"}

func tablesPresent(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var present []string
	for _, tbl := range appTables {
		var ok bool
		if err := db.QueryRow("SELECT to_regclass('public.' || $1) IS NOT NULL", tbl).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if ok {
			present = append(present, tbl)
		}
	}
	return present
}

func TestMigrateUpDownUp(t *testing.T) {
	ctx := context.Background()
	db := freshDB(t)

	pending := func(want bool) {
		t.Helper()
		if got, err := Pending(ctx, db); err != nil || got != want {
			t.Fatalf("Pending = %v, %v; want %v", got, err, want)
		}
	}
	pending(true)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	pending(false)
	if got := tablesPresent(t, db); !slices.Equal(got, appTables) {
		t.Fatalf("after up: tables %v, want %v", got, appTables)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second run is not a no-op: %v", err)
	}
	p, err := newMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	sources := p.ListSources()
	want := sources[len(sources)-1].Version // the highest embedded migration
	if v, err := p.GetDBVersion(ctx); err != nil || v != want {
		t.Fatalf("version %d, %v; want %d", v, err, want)
	}

	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := tablesPresent(t, db); len(got) != 0 {
		t.Fatalf("after down: tables left %v", got)
	}
	pending(true)
	var domains int
	if err := db.QueryRow("SELECT count(*) FROM pg_type WHERE typtype = 'd' AND typnamespace = 'public'::regnamespace").Scan(&domains); err != nil || domains != 0 {
		t.Fatalf("after down: %d domains left (%v)", domains, err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// pgCode returns the Postgres error code of err, or "" for success.
func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	if err != nil {
		return "not a Postgres error: " + err.Error()
	}
	return ""
}

const (
	notNull = "23502"
	fkey    = "23503"
	unique  = "23505"
	check   = "23514"
)

func TestConstraints(t *testing.T) {
	ctx := context.Background()
	db := freshDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("setup %q: %v", q, err)
		}
	}
	const (
		user  = "00000000-0000-4000-8000-000000000001"
		campA = "00000000-0000-4000-8000-0000000000a1"
		campB = "00000000-0000-4000-8000-0000000000b1"
		revA  = "00000000-0000-4000-8000-0000000000a2"
		revB  = "00000000-0000-4000-8000-0000000000b2"
	)
	insertUser := `INSERT INTO users (id, first_name, last_name, email, phone, city, language, loyalty_tier, signup_at, consent_sms, consent_email)
		VALUES ($1, 'Ayşe', 'Yılmaz', $2, '+90 000 000 0001', $3, $4, 'gold', now(), true, true)`
	mustExec(insertUser, user, "user1@example.com", "istanbul", "tr")
	mustExec(`INSERT INTO campaigns (id, brief) VALUES ($1, 'brief a'), ($2, 'brief b')`, campA, campB)
	insertRev := `INSERT INTO campaign_revisions (id, campaign_id, n, source, segment, sms_body, email_subject, email_body)
		VALUES ($1, $2, 1, 'llm', '{}', 'sms', 'subject', 'body')`
	mustExec(insertRev, revA, campA)
	mustExec(insertRev, revB, campB)
	insertMsg := `INSERT INTO messages (campaign_id, revision_id, user_id, channel) VALUES ($1, $2, $3, $4)`
	mustExec(insertMsg, campA, revA, user, "sms")

	cases := []struct {
		name  string
		query string
		args  []any
		want  string
	}{
		{"user city outside the catalog", insertUser, []any{"00000000-0000-4000-8000-000000000002", "u2@example.com", "paris", "tr"}, check},
		{"user language outside the catalog", insertUser, []any{"00000000-0000-4000-8000-000000000003", "u3@example.com", "istanbul", "de"}, check},
		{"user without a city", insertUser, []any{"00000000-0000-4000-8000-000000000004", "u4@example.com", nil, "tr"}, notNull},
		{"duplicate email", insertUser, []any{"00000000-0000-4000-8000-000000000005", "user1@example.com", "ankara", "tr"}, unique},
		{"event type outside the catalog", `INSERT INTO user_events (user_id, type, occurred_at) VALUES ($1, 'signup', now())`, []any{user}, check},
		{"second seed_meta row", `INSERT INTO seed_meta (id, as_of, seed, user_count) VALUES (false, now(), 1, 1)`, nil, check},
		{"approved without a revision", `UPDATE campaigns SET state = 'approved' WHERE id = $1`, []any{campA}, check},
		{"half an approval", `UPDATE campaigns SET approved_by = 'marketer' WHERE id = $1`, []any{campA}, check},
		{"approving another campaign's revision",
			`UPDATE campaigns SET state = 'approved', approved_revision_id = $2, approved_by = 'marketer', approved_at = now() WHERE id = $1`,
			[]any{campA, revB}, fkey},
		{"unknown campaign state", `UPDATE campaigns SET state = 'paused' WHERE id = $1`, []any{campA}, check},
		{"revision number 0", `INSERT INTO campaign_revisions (campaign_id, n, source, segment, sms_body, email_subject, email_body) VALUES ($1, 0, 'llm', '{}', 's', 's', 'b')`, []any{campA}, check},
		{"same message twice", insertMsg, []any{campA, revA, user, "sms"}, unique},
		{"message for another campaign's revision", insertMsg, []any{campA, revB, user, "email"}, fkey},
		{"unknown channel", insertMsg, []any{campA, revA, user, "push"}, check},
		{"retrying is not a state", `UPDATE messages SET state = 'retrying'`, nil, check},
		{"sent without a provider id", `UPDATE messages SET state = 'sent', sent_at = now()`, nil, check},
		{"failed without a reason", `UPDATE messages SET state = 'failed'`, nil, check},
		{"unknown reason label", `UPDATE messages SET last_error_class = 'oops'`, nil, check},
		{"negative attempts", `UPDATE messages SET attempts = -1`, nil, check},
	}
	for _, c := range cases {
		if got := pgCode(execErr(db, c.query, c.args...)); got != c.want {
			t.Errorf("%s: got %q, want %s", c.name, got, c.want)
		}
	}

	// The valid paths still work.
	valid := []struct {
		name  string
		query string
		args  []any
	}{
		{"approve own revision", `UPDATE campaigns SET state = 'approved', approved_revision_id = $2, approved_by = 'marketer', approved_at = now() WHERE id = $1`, []any{campA, revA}},
		{"mark sent with provider id", `UPDATE messages SET state = 'sent', provider_message_id = 'p-1', sent_at = now()`, nil},
		{"seed_meta first row", `INSERT INTO seed_meta (as_of, seed, user_count) VALUES (now(), 42, 1)`, nil},
	}
	for _, v := range valid {
		if err := execErr(db, v.query, v.args...); err != nil {
			t.Errorf("%s: %v", v.name, err)
		}
	}
}

func execErr(db *sql.DB, q string, args ...any) error {
	_, err := db.Exec(q, args...)
	return err
}

// TestCatalogChecksMatchGo keeps the database CHECK lists equal to the Go
// catalog, the third copy next to the JSON Schema.
func TestCatalogChecksMatchGo(t *testing.T) {
	db := freshDB(t)
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	quoted := regexp.MustCompile(`'([^']*)'::text`)
	cases := []struct {
		table, column string
		want          []string
	}{
		{"users", "city", segment.EnumValues("city")},
		{"users", "language", segment.EnumValues("language")},
		{"users", "loyalty_tier", segment.EnumValues("loyalty_tier")},
		{"user_events", "type", segment.EventTypes()},
	}
	for _, c := range cases {
		var def string
		err := db.QueryRow(`SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c
			JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
			WHERE c.conrelid = $1::regclass AND c.contype = 'c' AND a.attname = $2`, c.table, c.column).Scan(&def)
		if err != nil {
			t.Fatalf("%s.%s: %v", c.table, c.column, err)
		}
		var got []string
		for _, m := range quoted.FindAllStringSubmatch(def, -1) {
			got = append(got, m[1])
		}
		slices.Sort(got)
		want := slices.Sorted(slices.Values(c.want))
		if !slices.Equal(got, want) {
			t.Errorf("%s.%s CHECK has %v, Go catalog has %v", c.table, c.column, got, want)
		}
	}
}
