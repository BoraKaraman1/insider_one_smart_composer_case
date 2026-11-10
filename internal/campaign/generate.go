package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/retry"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

const WorkerActor = "worker"

const (
	MaxValidationAttempts = 3
	MaxTransportRetries   = 3
)

const DefaultDeltaInterval = 150 * time.Millisecond

const (
	ReasonRefusal       = "refusal"
	ReasonInvalidOutput = "invalid_output"
	ReasonTransport     = "transport_error"
	ReasonCancelled     = "cancelled"
)

type LLM interface {
	Call(ctx context.Context, conv *llm.Conversation, onText func(string)) (llm.Response, error)
	Model() string
}

type Pool interface {
	Beginner
	store.DB
}

type Generator struct {
	Pool          Pool
	Schemas       *schema.Set
	LLM           LLM
	Now           func() time.Time
	Rand          *rand.Rand
	Sleep         func(ctx context.Context, d time.Duration) error
	DeltaInterval time.Duration
	Log           *slog.Logger
	Metrics       Metrics

	mu sync.Mutex
}

type Metrics interface {
	LLMRequest(stage, outcome string, d time.Duration)
	LLMTokens(stage string, input, output int)
	LLMValidationFailure(stage, rule string)
	Generation(outcome string, d time.Duration)
	JobProcessed(at time.Time)
}

type noMetrics struct{}

func (noMetrics) LLMRequest(string, string, time.Duration) {}
func (noMetrics) LLMTokens(string, int, int)               {}
func (noMetrics) LLMValidationFailure(string, string)      {}
func (noMetrics) Generation(string, time.Duration)         {}
func (noMetrics) JobProcessed(time.Time)                   {}

func (g *Generator) metrics() Metrics {
	if g.Metrics != nil {
		return g.Metrics
	}
	return noMetrics{}
}

var errStop = errors.New("campaign: generation no longer current")

func (g *Generator) Run(ctx context.Context, args jobs.GenerateArgs) error {
	r := &genRun{g: g, id: args.CampaignID, version: args.Version}
	start := time.Now()
	err := r.run(ctx)
	met := g.metrics()
	if r.outcome != "" {
		met.Generation(r.outcome, time.Since(start))
	}
	met.JobProcessed(time.Now())
	if errors.Is(err, errStop) {
		return nil
	}
	if err != nil && ctx.Err() != nil {
		if ok, cerr := r.current(context.WithoutCancel(ctx)); cerr == nil && !ok {
			return nil
		}
	}
	return err
}

type genRun struct {
	g       *Generator
	id      string
	version int
	outcome string
}

func (r *genRun) log() *slog.Logger {
	if r.g.Log != nil {
		return r.g.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (r *genRun) current(ctx context.Context) (bool, error) {
	return store.LockGenerating(ctx, r.g.Pool, r.id, r.version)
}

func (r *genRun) write(ctx context.Context, fn func(pgx.Tx) error) error {
	return inTx(ctx, r.g.Pool, func(tx pgx.Tx) error {
		ok, err := store.LockGenerating(ctx, tx, r.id, r.version)
		if err != nil {
			return err
		}
		if !ok {
			return errStop
		}
		return fn(tx)
	})
}

func (r *genRun) event(ctx context.Context, typ string, payload any) error {
	return r.write(ctx, func(tx pgx.Tx) error {
		_, err := store.InsertGenerationEvent(ctx, tx, r.id, typ, payload)
		return err
	})
}

func (r *genRun) run(ctx context.Context) error {
	c, err := store.GetCampaign(ctx, r.g.Pool, r.id)
	if errors.Is(err, store.ErrNotFound) {
		return errStop
	}
	if err != nil {
		return err
	}
	if State(c.State) != Generating || c.Version != r.version {
		return errStop
	}
	asOf := r.g.Now().UTC().Truncate(time.Microsecond)

	var rule segment.Rule
	var ruleDoc any
	var segWarnings []validation.Issue
	out, err := r.stage(ctx, llm.NewConversation(llm.Segment, llm.SegmentTurn(c.Brief)), func(text string) validation.Result {
		doc, derr := schema.Decode(strings.NewReader(text))
		if derr != nil {
			var res validation.Result
			res.Error("", validation.InvalidJSON, "the answer is not one JSON value: "+derr.Error())
			return res
		}
		var res validation.Result
		rule, res = segment.Validate(r.g.Schemas, doc)
		ruleDoc, segWarnings = doc, res.Warnings
		return res
	}, nil)
	if err != nil || out != nil {
		return r.finish(ctx, err, out)
	}
	counts, err := segment.CountAudience(ctx, r.g.Pool, rule, asOf)
	if err != nil {
		return err
	}
	description := segment.Describe(rule)
	if err := r.event(ctx, "segment_ready", map[string]any{"rule": ruleDoc, "description": description, "counts": counts}); err != nil {
		return err
	}

	var cont content.Content
	var contWarnings []validation.Issue
	out, err = r.stage(ctx, llm.NewConversation(llm.Content, llm.ContentTurn(c.Brief, description)), func(text string) validation.Result {
		var res validation.Result
		cont, res = content.ValidateJSON(r.g.Schemas, []byte(text))
		if res.OK() {
			checkSubstance(cont, &res)
		}
		contWarnings = res.Warnings
		return res
	}, r.newDeltas)
	if err != nil || out != nil {
		return r.finish(ctx, err, out)
	}

	segJSON, err := json.Marshal(ruleDoc)
	if err != nil {
		return err
	}
	warnings := []validation.Issue{}
	for _, w := range segWarnings {
		warnings = append(warnings, validation.Issue{Path: "/segment" + w.Path, Code: w.Code, Message: w.Message})
	}
	for _, w := range contWarnings {
		warnings = append(warnings, validation.Issue{Path: "/content" + w.Path, Code: w.Code, Message: w.Message})
	}
	warnJSON, err := json.Marshal(warnings)
	if err != nil {
		return err
	}
	return r.wrote("done", r.write(ctx, func(tx pgx.Tx) error {
		if err := Transition(ctx, tx, Move{ID: r.id, From: Generating, To: InReview, Version: r.version, Actor: WorkerActor, Reason: "generated"}); err != nil {
			return err
		}
		rev, err := store.InsertRevision(ctx, tx, store.NewRevision{
			CampaignID: r.id, Source: "llm", Segment: segJSON,
			SMSBody: cont.SMSBody, EmailSubject: cont.EmailSubject, EmailBody: cont.EmailBody,
			Warnings: warnJSON, AudienceTotal: counts.Total, SMSEligible: counts.SMSEligible, EmailEligible: counts.EmailEligible,
		})
		if err != nil {
			return err
		}
		_, err = store.InsertGenerationEvent(ctx, tx, r.id, "done", map[string]any{"revision_id": rev.ID})
		return err
	}))
}

func (r *genRun) wrote(outcome string, err error) error {
	if err == nil {
		r.outcome = outcome
	}
	return err
}

type failure struct {
	stage  llm.Stage
	reason string
	errors []validation.Issue
}

func (r *genRun) finish(ctx context.Context, err error, f *failure) error {
	if err != nil {
		return err
	}
	errs := f.errors
	if errs == nil {
		errs = []validation.Issue{}
	}
	r.log().Warn("generation failed", "campaign_id", r.id, "stage", f.stage, "reason", f.reason, "errors", len(errs))
	return r.wrote("failed", r.write(ctx, func(tx pgx.Tx) error {
		if err := Transition(ctx, tx, Move{ID: r.id, From: Generating, To: GenerationFailed, Version: r.version,
			Actor: WorkerActor, Reason: f.reason}); err != nil {
			return err
		}
		_, err := store.InsertGenerationEvent(ctx, tx, r.id, "failed", map[string]any{"reason": f.reason, "stage": f.stage, "errors": errs})
		return err
	}))
}

func (r *genRun) stage(ctx context.Context, conv *llm.Conversation, validate func(string) validation.Result,
	deltas func(ctx context.Context, attempt int) *deltaWriter) (*failure, error) {
	stage := conv.Stage
	met := r.g.metrics()
	var last []validation.Issue
	for attempt := 1; attempt <= MaxValidationAttempts; attempt++ {
		var resp llm.Response
		var took time.Duration
		for try := 0; ; try++ {
			if ok, err := r.current(ctx); err != nil || !ok {
				return nil, orStop(err)
			}
			var dw *deltaWriter
			var onText func(string)
			if deltas != nil {
				dw = deltas(ctx, attempt)
				onText = dw.text
			}
			start := time.Now()
			var err error
			resp, err = r.g.LLM.Call(ctx, conv, onText)
			took = time.Since(start)
			if dw != nil {
				if ferr := dw.flush(); ferr != nil {
					return nil, ferr
				}
			}
			if err == nil {
				break
			}
			var ce *llm.CallError
			if !errors.As(err, &ce) {
				return nil, err
			}
			met.LLMRequest(string(stage), "transport_error", took)
			if werr := r.record(ctx, store.LLMCall{Stage: string(stage), Attempt: attempt, Outcome: "transport_error",
				ErrorClass: string(ce.Class), Model: r.g.LLM.Model(), LatencyMS: int(time.Since(start).Milliseconds())}); werr != nil {
				return nil, werr
			}
			// Yarım kalan metin okuyucuya silinsin diye işaretlenir.
			if dw != nil && dw.wrote {
				if werr := r.event(ctx, "attempt_failed", map[string]any{"stage": stage, "attempt": attempt,
					"reason": ReasonTransport, "class": ce.Class}); werr != nil {
					return nil, werr
				}
			}
			r.log().Warn("llm call failed", "campaign_id", r.id, "stage", stage, "attempt", attempt, "class", ce.Class, "status", ce.Status)
			if !ce.Class.Retryable() || try >= MaxTransportRetries {
				return &failure{stage: stage, reason: ReasonTransport, errors: []validation.Issue{{
					Code: string(ce.Class), Message: fmt.Sprintf("the call to the model failed: %s", ce.Class)}}}, nil
			}
			delay := ce.RetryAfter
			if delay == 0 {
				delay = r.g.backoff(try)
			}
			if err := r.g.Sleep(ctx, delay); err != nil {
				return nil, err
			}
		}

		row := store.LLMCall{Stage: string(stage), Attempt: attempt, StopReason: resp.StopReason, Model: resp.Model,
			InputTokens: &resp.InputTokens, OutputTokens: &resp.OutputTokens, LatencyMS: int(resp.Latency.Milliseconds()),
			RawOutput: resp.Text}
		if row.Model == "" {
			row.Model = r.g.LLM.Model()
		}
		met.LLMTokens(string(stage), resp.InputTokens, resp.OutputTokens)
		var issues []validation.Issue
		switch resp.StopReason {
		case "refusal":
			row.Outcome = "refusal"
			met.LLMRequest(string(stage), row.Outcome, took)
			if err := r.record(ctx, row); err != nil {
				return nil, err
			}
			return &failure{stage: stage, reason: ReasonRefusal, errors: []validation.Issue{{
				Code: "refusal", Message: "the model refused to answer"}}}, nil
		case "max_tokens":
			row.Outcome = "max_tokens"
			issues = []validation.Issue{{Code: "max_tokens", Message: "the answer was cut off at the token limit; answer again with shorter copy"}}
		default:
			res := validate(resp.Text)
			if res.OK() {
				row.Outcome = "valid"
				met.LLMRequest(string(stage), row.Outcome, took)
				return nil, r.record(ctx, row)
			}
			row.Outcome = "invalid"
			issues = res.Errors
			seen := map[string]bool{}
			for _, is := range issues {
				if !seen[is.Code] {
					seen[is.Code] = true
					met.LLMValidationFailure(string(stage), is.Code)
				}
			}
		}
		met.LLMRequest(string(stage), row.Outcome, took)
		row.ValidationErrors = issues
		if err := r.record(ctx, row); err != nil {
			return nil, err
		}
		if err := r.event(ctx, "attempt_failed", map[string]any{"stage": stage, "attempt": attempt, "errors": issues}); err != nil {
			return nil, err
		}
		r.log().Warn("llm output invalid", "campaign_id", r.id, "stage", stage, "attempt", attempt, "errors", len(issues))
		last = issues
		conv.Repair(resp, issues)
	}
	return &failure{stage: stage, reason: ReasonInvalidOutput, errors: last}, nil
}

func orStop(err error) error {
	if err != nil {
		return err
	}
	return errStop
}

func (r *genRun) record(ctx context.Context, c store.LLMCall) error {
	c.CampaignID = r.id
	c.PromptVersion = llm.PromptVersion(llm.Stage(c.Stage))
	return r.write(ctx, func(tx pgx.Tx) error { return store.InsertLLMCall(ctx, tx, c) })
}

func (g *Generator) backoff(try int) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return retry.Backoff(try, g.Rand)
}

type deltaWriter struct {
	r        *genRun
	ctx      context.Context
	attempt  int
	interval time.Duration
	buf      bytes.Buffer
	sent     llm.Fields
	last     time.Time
	err      error
	wrote    bool
}

func (r *genRun) newDeltas(ctx context.Context, attempt int) *deltaWriter {
	return &deltaWriter{r: r, ctx: ctx, attempt: attempt, interval: r.g.DeltaInterval, last: time.Now()}
}

func (d *deltaWriter) text(s string) {
	d.buf.WriteString(s)
	if d.err == nil && time.Since(d.last) >= d.interval {
		d.err = d.emit()
	}
}

func (d *deltaWriter) flush() error {
	if d.err != nil {
		return d.err
	}
	return d.emit()
}

func (d *deltaWriter) emit() error {
	d.last = time.Now()
	cur := llm.ExtractFields(d.buf.String())
	for _, f := range []struct {
		name      string
		now, sent *string
	}{
		{"sms.body", &cur.SMSBody, &d.sent.SMSBody},
		{"email.subject", &cur.EmailSubject, &d.sent.EmailSubject},
		{"email.body", &cur.EmailBody, &d.sent.EmailBody},
	} {
		if len(*f.now) <= len(*f.sent) || !strings.HasPrefix(*f.now, *f.sent) {
			continue
		}
		if err := d.r.event(d.ctx, "delta", map[string]any{"stage": llm.Content, "attempt": d.attempt,
			"field": f.name, "text": (*f.now)[len(*f.sent):]}); err != nil {
			return err
		}
		*f.sent = *f.now
		d.wrote = true
	}
	return nil
}

type GenerateWorker struct {
	river.WorkerDefaults[jobs.GenerateArgs]
	g *Generator
}

func NewGenerateWorker(g *Generator) *GenerateWorker { return &GenerateWorker{g: g} }

func (w *GenerateWorker) Timeout(*river.Job[jobs.GenerateArgs]) time.Duration {
	return jobs.GenerateTimeout
}

func (w *GenerateWorker) Work(ctx context.Context, job *river.Job[jobs.GenerateArgs]) error {
	return w.g.Run(ctx, job.Args)
}

type Inserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

func StartGeneration(ctx context.Context, db Beginner, ins Inserter, id string, version int, actor string) (jobs.GenerateArgs, error) {
	args, _, err := BeginGeneration(ctx, db, ins, id, version, actor)
	return args, err
}

func BeginGeneration(ctx context.Context, db Beginner, ins Inserter, id string, version int, actor string) (jobs.GenerateArgs, int64, error) {
	var args jobs.GenerateArgs
	var started int64
	err := inTx(ctx, db, func(tx pgx.Tx) error {
		c, err := store.GetCampaign(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := Transition(ctx, tx, Move{ID: id, From: State(c.State), To: Generating, Version: version, Actor: actor, Reason: "generate"}); err != nil {
			return err
		}
		args = jobs.GenerateArgs{CampaignID: id, Version: version + 1}
		if started, err = store.InsertGenerationEvent(ctx, tx, id, "started", map[string]any{"version": args.Version}); err != nil {
			return err
		}
		_, err = ins.InsertTx(ctx, tx, args, nil)
		return err
	})
	return args, started, err
}

type JobCanceller interface {
	JobCancelTx(ctx context.Context, tx pgx.Tx, jobID int64) (*rivertype.JobRow, error)
}

func CancelWithJobs(ctx context.Context, db Beginner, jc JobCanceller, id string, version int, actor string) error {
	return inTx(ctx, db, func(tx pgx.Tx) error {
		if err := cancelTx(ctx, tx, id, version, actor); err != nil {
			return err
		}
		if jc == nil {
			return nil
		}
		ids, err := store.UnfinishedJobs(ctx, tx, jobs.GenerateArgs{}.Kind(), id)
		if err != nil {
			return err
		}
		for _, jobID := range ids {
			if _, err := jc.JobCancelTx(ctx, tx, jobID); err != nil && !errors.Is(err, rivertype.ErrNotFound) {
				return err
			}
		}
		return nil
	})
}
