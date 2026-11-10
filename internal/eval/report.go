package eval

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func WriteReport(path string, rep Report) error {
	var data []byte
	if strings.EqualFold(filepath.Ext(path), ".json") {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		data = append(b, '\n')
	} else {
		data = []byte(rep.Markdown())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (r Report) Markdown() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	p("# Eval report\n\n")
	p("| | |\n|---|---|\n")
	p("| mode | %s |\n| model | %s |\n", r.Mode, r.Model)
	for _, st := range slices.Sorted(maps.Keys(r.PromptVersions)) {
		p("| prompt_version %s | %s |\n", st, r.PromptVersions[st])
	}
	p("| seed as_of | %s |\n| runs | %d |\n| generations | %d |\n\n", r.AsOf.Format("2006-01-02T15:04:05Z07:00"), r.Runs, r.Numbers.Generations)
	if r.Mode == "mock" {
		p("Mock mode: these numbers prove the harness, not the model.\n\n")
	}

	p("## Correct by category\n\n| category | correct | total |\n|---|---|---|\n")
	for _, c := range slices.Sorted(maps.Keys(r.Correct)) {
		p("| %s | %d | %d |\n", c, r.Correct[c].Correct, r.Correct[c].Total)
	}
	p("\n## Injection cases\n\n| outcome | count |\n|---|---|\n")
	for _, o := range []InjectionOutcome{InjectionRefused, InjectionSafeValid, InjectionUnsafe} {
		p("| %s | %d |\n", o, r.Injection[o])
	}

	p("\n## Stages\n\n| stage | first-attempt valid | valid after repair | failed | validation attempts | refusals | transport retries | latency p50 ms | latency p95 ms |\n")
	p("|---|---|---|---|---|---|---|---|---|\n")
	for _, st := range slices.Sorted(maps.Keys(r.Numbers.Stages)) {
		s := r.Numbers.Stages[st]
		p("| %s | %d | %d | %d | %d | %d | %s | %d | %d |\n", st, s.FirstAttemptValid, s.ValidAfterRepair, s.Failed,
			s.ValidationAttempts, s.Refusals, counts(s.TransportRetries), s.LatencyP50MS, s.LatencyP95MS)
	}
	p("\n## Failure reasons by validation code\n\n| stage | code | count |\n|---|---|---|\n")
	for _, st := range slices.Sorted(maps.Keys(r.Numbers.Stages)) {
		codes := r.Numbers.Stages[st].FailureCodes
		for _, c := range slices.Sorted(maps.Keys(codes)) {
			p("| %s | %s | %d |\n", st, c, codes[c])
		}
	}

	p("\n## Time and tokens\n\n| | |\n|---|---|\n")
	p("| generation p50 ms | %d |\n| generation p95 ms | %d |\n", r.GenerationP50MS, r.GenerationP95MS)
	p("| input tokens | %d |\n| output tokens | %d |\n", r.Numbers.InputTokens, r.Numbers.OutputTokens)
	if g := r.Numbers.Generations; g > 0 {
		p("| input tokens per generation | %d |\n| output tokens per generation | %d |\n", r.Numbers.InputTokens/g, r.Numbers.OutputTokens/g)
	}

	p("\n## Cases\n\n| case | category | run | outcome | correct | injection | failed checks | audience total | generation ms |\n")
	p("|---|---|---|---|---|---|---|---|---|\n")
	for _, c := range r.Cases {
		failed := make([]string, len(c.Result.Failed))
		for i, f := range c.Result.Failed {
			failed[i] = string(f)
		}
		p("| %s | %s | %d | %s | %t | %s | %s | %d | %d |\n", c.Name, c.Category, c.Run, c.Outcome, c.Result.Correct,
			c.Result.Injection, strings.Join(failed, ", "), c.Total, c.GenerationMS)
	}
	return b.String()
}

func counts(m map[string]int) string {
	if len(m) == 0 {
		return "0"
	}
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, fmt.Sprintf("%s: %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
