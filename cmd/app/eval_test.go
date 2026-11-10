package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestEvalCmdFailsClosed(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	if err := evalCmd(context.Background(), log, envOf(nil), nil); err == nil || !strings.Contains(err.Error(), "EVAL_ADMIN_URL") {
		t.Errorf("no admin url: %v", err)
	}
	env := envOf(map[string]string{"EVAL_ADMIN_URL": "postgres://unused", "LLM_MODE": "other"})
	if err := evalCmd(context.Background(), log, env, nil); err == nil {
		t.Error("unknown LLM_MODE accepted")
	}
	env = envOf(map[string]string{"EVAL_ADMIN_URL": "postgres://unused", "LLM_MODE": "anthropic"})
	if err := evalCmd(context.Background(), log, env, nil); err == nil {
		t.Error("anthropic mode without a key accepted")
	}
	if err := evalCmd(context.Background(), log, envOf(map[string]string{"EVAL_ADMIN_URL": "x"}), []string{"-runs", "0"}); err == nil {
		t.Error("-runs 0 accepted")
	}
}

// LLM_MODE unset means mock: an in-process mockllm, a markdown report.
func TestEvalCmdMock(t *testing.T) {
	testdb.Fresh(t) // skips, or fails under REQUIRE_DB=1, like other integration tests
	tmpl := os.Getenv("TEST_TEMPLATE_DB")
	if tmpl == "" {
		tmpl = "app_template"
	}
	out := filepath.Join(t.TempDir(), "report.md")
	env := envOf(map[string]string{"EVAL_ADMIN_URL": os.Getenv("TEST_DATABASE_URL")})
	err := evalCmd(context.Background(), slog.New(slog.DiscardHandler), env,
		[]string{"-cases", "../../evals/briefs.json", "-out", out, "-template", tmpl})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "| mode | mock |") {
		t.Errorf("report header:\n%s", b)
	}
}
