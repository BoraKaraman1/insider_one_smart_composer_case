package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Stage 0 of the third plan: a minimal CI workflow that runs what make lint
// and make test run. These checks read the files as text, so they need no
// YAML library. They fail until .github/workflows/ci.yml exists.

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(b)
}

// seedArgs is the seed command line of the integration template.
var seedArgs = regexp.MustCompile(`seed -as-of (\S+) -seed (\d+) -users (\d+)`)

func TestCIWorkflowGoJob(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")

	for _, want := range []struct{ what, re string }{
		{"a go job", `(?m)^  go:\s*$`},
		{"Go from go.mod, never a hard-coded version", `go-version-file:\s*['"]?go\.mod`},
		{"a gofmt check", `gofmt -l`},
		{"go vet", `go vet \./\.\.\.`},
		{"a postgres service container", `(?m)^\s+services:\s*$[\s\S]*image:\s*['"]?postgres`},
		{"REQUIRE_DB=1, so a missing database fails instead of skipping", `REQUIRE_DB:?\s*['"]?1|REQUIRE_DB=1`},
		{"the migrate step", `app migrate`},
		{"go test over every package", `go test [^\n]*\./\.\.\.`},
		{"the hook tests", `guard-secrets_test\.sh|make (-s )?test-hooks`},
	} {
		if !regexp.MustCompile(want.re).MatchString(ci) {
			t.Errorf("ci.yml has no %s (want a match for %q)", want.what, want.re)
		}
	}
	if regexp.MustCompile(`go-version:\s`).MatchString(ci) {
		t.Error("ci.yml pins go-version; use go-version-file: go.mod so TestToolchainPinned stays the one source")
	}
}

// The template is seeded exactly as make test seeds it, so the expected
// counts in docs/seed-data.md hold in CI too.
func TestCISeedsLikeMakeTest(t *testing.T) {
	mk := seedArgs.FindStringSubmatch(readRepoFile(t, "Makefile"))
	if mk == nil {
		t.Fatal("Makefile has no seed command with -as-of, -seed and -users")
	}
	ci := seedArgs.FindStringSubmatch(readRepoFile(t, ".github/workflows/ci.yml"))
	if ci == nil {
		t.Fatal("ci.yml has no seed command with -as-of, -seed and -users")
	}
	if strings.Join(mk[1:], " ") != strings.Join(ci[1:], " ") {
		t.Errorf("ci.yml seeds with %v, make test with %v", ci[1:], mk[1:])
	}
}

// Rule 1: CI never has the API key. It runs mock mode only.
func TestCIHasNoAPIKey(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	if strings.Contains(ci, "ANTHROPIC_API_KEY") && !regexp.MustCompile(`ANTHROPIC_API_KEY:\s*['"]{2}`).MatchString(ci) {
		t.Error("ci.yml mentions ANTHROPIC_API_KEY other than to set it empty")
	}
	if strings.Contains(ci, "secrets.") {
		t.Error("ci.yml reads a repository secret; the tests need none")
	}
	if regexp.MustCompile(`LLM_MODE:?\s*['"]?anthropic`).MatchString(ci) {
		t.Error("ci.yml runs LLM_MODE=anthropic")
	}
}

// Stream 1 adds the web job as soon as web/ exists.
func TestCIWebJobOnceWebExists(t *testing.T) {
	if _, err := os.Stat("../../web/package.json"); err != nil {
		t.Skip("web/ does not exist yet")
	}
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	for _, re := range []string{`(?m)^  web:\s*$`, `npm ci`, `working-directory:\s*['"]?web`} {
		if !regexp.MustCompile(re).MatchString(ci) {
			t.Errorf("web/ exists but ci.yml has no match for %q", re)
		}
	}
}
