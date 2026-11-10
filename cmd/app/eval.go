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

// Altın brifler çalışır ve rapor yazılır, uygulama veritabanına dokunulmaz.
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
