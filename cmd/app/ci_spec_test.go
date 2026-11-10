package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(b)
}

var seedArgs = regexp.MustCompile(`seed -as-of (\S+) -seed (\d+) -users (\d+)`)

func TestCIWorkflowGoJob(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")

	for _, want := range []struct{ what, re string }{
		{"a lint-test job", `(?m)^  lint-test:\s*$`},
		{"an integration job", `(?m)^  integration:\s*$`},
		{"Go from go.mod, never a hard-coded version", `go-version-file:\s*['"]?go\.mod`},
		{"a gofmt check", `gofmt -l`},
		{"go vet", `go vet \./\.\.\.`},
		{"a postgres service container", `(?m)^\s+services:\s*$[\s\S]*image:\s*['"]?postgres`},
		{"REQUIRE_DB=1, so a missing database fails instead of skipping", `REQUIRE_DB:?\s*['"]?1|REQUIRE_DB=1`},
		{"the migrate step", `app migrate`},
		{"go test over every package", `go test [^\n]*\./\.\.\.`},
		{"the hook tests", `guard-secrets_test\.sh|make (-s )?test-hooks`},
		{"race on a full test run", `go test -race [^\n]*\./\.\.\.`},
	} {
		if !regexp.MustCompile(want.re).MatchString(ci) {
			t.Errorf("ci.yml has no %s (want a match for %q)", want.what, want.re)
		}
	}
	if regexp.MustCompile(`go-version:\s`).MatchString(ci) {
		t.Error("ci.yml pins go-version; use go-version-file: go.mod so TestToolchainPinned stays the one source")
	}
}

func TestCIStream4(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	jobs := []string{"lint-test", "integration", "fuzz", "web", "eval", "docker", "secrets"}
	for _, job := range jobs {
		block := regexp.MustCompile(`(?m)^  ` + job + `:\n(?:    .*\n)*`).FindString(ci)
		if block == "" {
			t.Errorf("no job %s", job)
			continue
		}
		if !strings.Contains(block, "timeout-minutes:") {
			t.Errorf("job %s has no timeout-minutes", job)
		}
	}
	if !strings.Contains(ci, "permissions:") || !strings.Contains(ci, "contents: read") {
		t.Error("ci.yml does not set permissions contents: read")
	}
	uses := regexp.MustCompile(`(?m)^\s*(?:- )?uses:\s*(\S+)`)
	found := uses.FindAllStringSubmatch(ci, -1)
	if len(found) == 0 {
		t.Fatal("ci.yml has no actions")
	}
	sha := regexp.MustCompile(`@[0-9a-f]{40}$`)
	for _, m := range found {
		if !sha.MatchString(m[1]) {
			t.Errorf("action is not pinned to a commit SHA: %s", m[1])
		}
	}
	for _, want := range []string{
		"staticcheck@v0.8.2",
		"govulncheck@v1.9.0",
		"-count=3",
		"-timeout 5m",
		"FuzzExtractFieldsNeverPanics",
		"FuzzExtractFieldsPrefix",
		"FuzzCompileShape",
		"FuzzParse",
		"fuzztime=30s",
		"Failing input written to",
		"fetch-depth: 0",
		"gitleaks/gitleaks-action@",
		"push: false",
		"load: true",
		"make e2e",
		"upload-artifact@",
		"evals/report-mock.md",
	} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml is missing %q", want)
		}
	}
}

// Tohumlama yerel testle aynıdır ki beklenen sayılar derlemede de tutsun.
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

// Derleme anahtar taşımaz ve yalnız sahte kipte çalışır.
func TestCIHasNoAPIKey(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	if strings.Contains(ci, "ANTHROPIC_API_KEY") && !regexp.MustCompile(`ANTHROPIC_API_KEY:\s*['"]{2}`).MatchString(ci) {
		t.Error("ci.yml mentions ANTHROPIC_API_KEY other than to set it empty")
	}
	if strings.Contains(ci, "secrets.") {
		t.Error("ci.yml reads a repository secret; the tests need none")
	}
	if regexp.MustCompile(`Authorization:\s+Bearer\s+[A-Za-z0-9]`).MatchString(ci) {
		t.Error("ci.yml has an Authorization bearer token; gitleaks curl-auth-header matches that curl form")
	}
	if regexp.MustCompile(`LLM_MODE:?\s*['"]?anthropic`).MatchString(ci) {
		t.Error("ci.yml runs LLM_MODE=anthropic")
	}
}

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
