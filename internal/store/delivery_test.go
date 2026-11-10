package store

import (
	"database/sql"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func TestMessagesHaveFirstAttemptAt(t *testing.T) {
	url, _ := testdb.Seeded(t)
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var typ, nullable string
	err = db.QueryRow(`SELECT data_type, is_nullable FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'messages' AND column_name = 'first_attempt_at'`).Scan(&typ, &nullable)
	if err == sql.ErrNoRows {
		t.Fatal("messages.first_attempt_at does not exist (migration 00004)")
	}
	if err != nil {
		t.Fatal(err)
	}
	if typ != "timestamp with time zone" || nullable != "YES" {
		t.Errorf("first_attempt_at is %s, nullable %s; want a nullable timestamptz", typ, nullable)
	}
	var def sql.NullString
	if err := db.QueryRow(`SELECT column_default FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'messages' AND column_name = 'first_attempt_at'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if def.Valid {
		t.Errorf("first_attempt_at has default %q; it must stay empty until the first attempt", def.String)
	}
}
