package eval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"net/url"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

// Config is one eval run.
type Config struct {
	AdminURL string // admin connection to the server that holds Template
	Template string // a migrated and seeded database to clone
	Cases    []Case
	Runs     int // times each case runs; at least 1
	LLM      campaign.LLM
	Mode     string // "mock" or "anthropic", for the report header
	// Sleep waits out transport backoff. Nil waits on the wall clock.
	Sleep func(context.Context, time.Duration) error
	Log   *slog.Logger // nil discards
}

// Report is the result of a run. It holds no key and no recipient data.
type Report struct {
	Mode            string                   `json:"mode"`
	Model           string                   `json:"model"`
	PromptVersions  map[string]string        `json:"prompt_versions"`
	AsOf            time.Time                `json:"as_of"`
	Runs            int                      `json:"runs"`
	Numbers         Numbers                  `json:"numbers"`
	Cases           []CaseResult             `json:"cases"`
	Correct         map[string]Tally         `json:"correct"`   // by category, injection cases left out
	Injection       map[InjectionOutcome]int `json:"injection"` // injection cases only
	GenerationP50MS int                      `json:"generation_p50_ms"`
	GenerationP95MS int                      `json:"generation_p95_ms"`
}

// Tally counts correct results out of a total.
type Tally struct {
	Correct int `json:"correct"`
	Total   int `json:"total"`
}

// CaseResult is one case in one run.
type CaseResult struct {
	Name         string `json:"name"`
	Category     string `json:"category"`
	Run          int    `json:"run"`
	Outcome      string `json:"outcome"`
	Result       Result `json:"result"`
	Total        int    `json:"audience_total"`
	GenerationMS int    `json:"generation_ms"`
}

// Run clones the template into its own eval_ database, inserts the
// fixtures, runs every case through campaign.Generator.Run in order and
// scores it. The clone is dropped on return, also on error. Generator.Now
// is the clone's seed_meta.as_of, so the run fails before any case without
// a seed_meta row.
func Run(ctx context.Context, cfg Config) (rep Report, err error) {
	if cfg.Runs < 1 {
		cfg.Runs = 1
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "eval_" + hex.EncodeToString(b)
	if err := store.CloneDatabase(ctx, cfg.AdminURL, cfg.Template, name); err != nil {
		return Report{}, fmt.Errorf("eval: clone %s: %w", cfg.Template, err)
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if derr := store.DropDatabase(dctx, cfg.AdminURL, name); derr != nil {
			err = errors.Join(err, fmt.Errorf("eval: drop %s: %w", name, derr))
		}
	}()

	u, err := url.Parse(cfg.AdminURL)
	if err != nil {
		return Report{}, err
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		return Report{}, err
	}
	defer pool.Close()

	asOf, err := store.SeededAt(ctx, pool)
	if err != nil {
		return Report{}, fmt.Errorf("eval: read seed_meta of %s: %w", cfg.Template, err)
	}
	if asOf == nil {
		return Report{}, fmt.Errorf("eval: template %s has no seed_meta row; seed it at a fixed anchor first", cfg.Template)
	}
	set, err := schemas()
	if err != nil {
		return Report{}, err
	}
	fx, err := InsertFixtures(ctx, pool, *asOf)
	if err != nil {
		return Report{}, fmt.Errorf("eval: fixtures: %w", err)
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = wallSleep
	}
	gen := &campaign.Generator{
		Pool: pool, Schemas: set, LLM: cfg.LLM, Log: log, Sleep: sleep,
		Now:  func() time.Time { return *asOf },
		Rand: mrand.New(mrand.NewPCG(1, 2)),
	}

	rep = Report{
		Mode: cfg.Mode, Model: cfg.LLM.Model(), AsOf: *asOf, Runs: cfg.Runs,
		PromptVersions: map[string]string{
			string(llm.Segment): llm.PromptVersion(llm.Segment),
			string(llm.Content): llm.PromptVersion(llm.Content),
		},
		Correct: map[string]Tally{}, Injection: map[InjectionOutcome]int{},
	}
	var rows []CallRow
	var durations []int
	for run := 1; run <= cfg.Runs; run++ {
		for _, c := range cfg.Cases {
			cr, calls, err := runCase(ctx, gen, pool, c, fx, *asOf)
			if err != nil {
				return Report{}, fmt.Errorf("eval: case %s, run %d: %w", c.Name, run, err)
			}
			cr.Run = run
			log.Info("eval case", "case", c.Name, "run", run, "outcome", cr.Outcome,
				"correct", cr.Result.Correct, "injection", cr.Result.Injection)
			rep.Cases = append(rep.Cases, cr)
			rows = append(rows, calls...)
			durations = append(durations, cr.GenerationMS)
			if c.Injection {
				rep.Injection[cr.Result.Injection]++
				continue
			}
			t := rep.Correct[c.Category]
			t.Total++
			if cr.Result.Correct {
				t.Correct++
			}
			rep.Correct[c.Category] = t
		}
	}
	rep.Numbers = Aggregate(rows)
	rep.GenerationP50MS, rep.GenerationP95MS = percentile(durations, 50), percentile(durations, 95)
	return rep, nil
}

// runCase creates a campaign for the brief, moves it to generating through
// the state machine (no River job is queued) and runs the generator.
func runCase(ctx context.Context, gen *campaign.Generator, pool *pgxpool.Pool, c Case, fx Fixtures, asOf time.Time) (CaseResult, []CallRow, error) {
	cr := CaseResult{Name: c.Name, Category: c.Category}
	camp, err := store.CreateCampaign(ctx, pool, c.Brief, "https://example.invalid/eval")
	if err != nil {
		return cr, nil, err
	}
	args, err := campaign.StartGeneration(ctx, pool, noQueue{}, camp.ID, camp.Version, "eval")
	if err != nil {
		return cr, nil, err
	}
	start := time.Now()
	if err := gen.Run(ctx, args); err != nil {
		return cr, nil, err
	}
	// Rounded up, so a generation always counts as at least 1 ms.
	cr.GenerationMS = int((time.Since(start) + time.Millisecond - 1) / time.Millisecond)
	if err := ctx.Err(); err != nil {
		return cr, nil, err
	}

	calls, err := store.ListLLMCalls(ctx, pool, camp.ID)
	if err != nil {
		return cr, nil, err
	}
	camp, err = store.GetCampaign(ctx, pool, camp.ID)
	if err != nil {
		return cr, nil, err
	}
	in := ScoreInput{Case: c, Fixtures: fx}
	switch {
	case campaign.State(camp.State) == campaign.InReview && camp.CurrentRevisionID != nil:
		in.Outcome = OutcomeValid
	case slices.ContainsFunc(calls, func(r CallRow) bool { return r.Outcome == "refusal" }):
		in.Outcome = OutcomeRefused
	default:
		in.Outcome = OutcomeFailed
	}
	if in.Outcome == OutcomeValid {
		rev, err := store.GetRevision(ctx, pool, camp.ID, *camp.CurrentRevisionID)
		if err != nil {
			return cr, nil, err
		}
		set, err := schemas()
		if err != nil {
			return cr, nil, err
		}
		rule, res := segment.ValidateJSON(set, rev.Segment)
		if !res.OK() {
			return cr, nil, fmt.Errorf("stored rule does not validate: %v", res.Errors)
		}
		if in.Audience, err = Audiences(ctx, pool, rule, asOf); err != nil {
			return cr, nil, err
		}
		counts, err := segment.CountAudience(ctx, pool, rule, asOf)
		if err != nil {
			return cr, nil, err
		}
		in.Rule, in.Total = rule, counts.Total
		in.Copy = content.Content{SMSBody: rev.SMSBody, EmailSubject: rev.EmailSubject, EmailBody: rev.EmailBody}
	}
	cr.Outcome, cr.Total = in.Outcome, in.Total
	cr.Result = Score(in)
	return cr, calls, nil
}

// noQueue stands in for River: StartGeneration inserts no job, and the
// harness calls Generator.Run itself.
type noQueue struct{}

func (noQueue) InsertTx(context.Context, pgx.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return &rivertype.JobInsertResult{}, nil
}

func wallSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
