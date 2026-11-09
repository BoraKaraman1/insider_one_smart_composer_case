// Package testdb gives integration tests their own Postgres database, so
// packages can run in parallel without touching each other's data.
//
// Tests need TEST_DATABASE_URL (an admin connection to the test server).
// Without it they skip, unless REQUIRE_DB=1, which make test sets: then a
// missing database fails the run instead of silently skipping it.
package testdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// Fresh creates an empty database for the calling test, drops it when the
// test ends, and returns its connection URL.
func Fresh(t testing.TB) string {
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
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
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
