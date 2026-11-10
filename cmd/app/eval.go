package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http/httptest"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/eval"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockllm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
)

// evalCmd runs the golden briefs and writes a report (plan 3, stream 2):
//
//	app eval [-cases evals/briefs.json] [-out evals/report-<mode>.md] [-runs 1] [-template app_template]
//
// EVAL_ADMIN_URL is an admin connection to the server that holds the
// seeded template. The run clones the template and drops the clone; it
// never touches the app database. LLM_MODE defaults to mock, which starts
// an in-process mockllm and never reads the key. LLM_MODE=anthropic is for
// the user's own terminal only (rule 1). An -out ending in .json writes
// JSON, anything else markdown.
func evalCmd(ctx context.Context, log *slog.Logger, getenv func(string) string, args []string) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	casesPath := fs.String("cases", "evals/briefs.json", "golden briefs file")
	out := fs.String("out", "", "report file (default evals/report-<mode>.md)")
	runs := fs.Int("runs", 1, "times each case runs")
	template := fs.String("template", "app_template", "seeded template database to clone")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runs < 1 {
		return errors.New("eval: -runs must be at least 1")
	}
	admin := getenv("EVAL_ADMIN_URL")
	if admin == "" {
		return errors.New("EVAL_ADMIN_URL is not set (an admin connection to the server with the seeded template)")
	}
	mode := getenv("LLM_MODE")
	if mode == "" {
		mode = "mock"
	}
	env := func(k string) string {
		if k == "LLM_MODE" {
			return mode
		}
		return getenv(k)
	}
	cfg, err := llm.ConfigFromEnv(env)
	if err != nil {
		return err
	}
	if mode == "mock" {
		srv := httptest.NewServer(mockllm.New(mockllm.Options{}))
		defer srv.Close()
		cfg.BaseURL = srv.URL
	}
	if cfg.Schemas, err = schema.Load(); err != nil {
		return err
	}
	client, err := llm.New(cfg)
	if err != nil {
		return err
	}
	cases, err := eval.LoadCases(*casesPath)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = "evals/report-" + mode + ".md"
	}

	rep, err := eval.Run(ctx, eval.Config{AdminURL: admin, Template: *template, Cases: cases, Runs: *runs,
		LLM: client, Mode: mode, Sleep: sleep, Log: log})
	if err != nil {
		return err
	}
	if err := eval.WriteReport(*out, rep); err != nil {
		return err
	}
	log.Info("eval finished", "mode", rep.Mode, "model", rep.Model, "generations", rep.Numbers.Generations,
		"report", *out)
	return nil
}
