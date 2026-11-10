// Paket her bütünleşme testine ayrı bir veritabanı verir.
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Bu kilit mağaza paketindeki kopyalama kilidiyle aynı kalmalı.
const cloneLock int64 = 0x7e57db

func Fresh(t testing.TB) string {
	t.Helper()
	return create(t, "")
}

type Seed struct {
	AsOf  time.Time
	Seed  int64
	Users int
}

func Seeded(t testing.TB) (string, Seed) {
	t.Helper()
	template := os.Getenv("TEST_TEMPLATE_DB")
	if template == "" {
		template = "app_template"
	}
	u := create(t, template)

	db, err := sql.Open("pgx", u)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var s Seed
	if err := db.QueryRow("SELECT as_of, seed, user_count FROM seed_meta").Scan(&s.AsOf, &s.Seed, &s.Users); err != nil {
		t.Fatalf("template %s has no seed_meta row (build it with make test): %v", template, err)
	}
	s.AsOf = s.AsOf.UTC()
	return u, s
}

func create(t testing.TB, template string) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		if os.Getenv("REQUIRE_DB") == "1" {
			t.Fatal("REQUIRE_DB=1 but TEST_DATABASE_URL is not set")
		}
		t.Skip("TEST_DATABASE_URL is not set; run the integration tests with make test")
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "test_" + hex.EncodeToString(b)

	ctx := context.Background()
	conn, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stmt := "CREATE DATABASE " + name
	if template != "" {
		stmt += " TEMPLATE " + pgx.Identifier{template}.Sanitize()
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", cloneLock); err != nil {
			t.Fatal(err)
		}
	}
	_, createErr := conn.ExecContext(ctx, stmt)
	if template != "" {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", cloneLock)
	}
	conn.Close()
	if createErr != nil {
		t.Fatalf("%s: %v (the template is built by make test)", stmt, createErr)
	}

	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
		admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
