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

// WorkerActor is the actor of transitions the generate job makes.
const WorkerActor = "worker"

// Retry limits per stage (rule 5, rule 6). They are counted separately: a
// transport failure does not use up a validation attempt.
const (
	MaxValidationAttempts = 3
	MaxTransportRetries   = 3
)

// DefaultDeltaInterval is how often streamed copy is written to the event
// log, per field. The worker sets it; zero writes every delta.
const DefaultDeltaInterval = 150 * time.Millisecond

// Reasons in the failed event and in the audit trail.
const (
	ReasonRefusal       = "refusal"
	ReasonInvalidOutput = "invalid_output"
	ReasonTransport     = "transport_error"
	ReasonCancelled     = "cancelled"
)

// LLM is what the generator needs from the model client. *llm.Client
// satisfies it.
type LLM interface {
	Call(ctx context.Context, conv *llm.Conversation, onText func(string)) (llm.Response, error)
	Model() string
}

// Pool is a database with transactions. *pgxpool.Pool satisfies it.
type Pool interface {
	Beginner
	store.DB
}

// Generator runs the generate job: the segment stage, then the content
// stage, each validated and repaired, and records every call and event.
type Generator struct {
	Pool    Pool
	Schemas *schema.Set
	LLM     LLM
	Now     func() time.Time // the asOf of audience counts
	Rand    *rand.Rand       // backoff jitter; shared, so guarded by mu
	Sleep   func(ctx context.Context, d time.Duration) error
	// DeltaInterval batches streamed copy per field (DefaultDeltaInterval
	// in the worker). Zero writes every delta.
	DeltaInterval time.Duration
	Log           *slog.Logger

	mu sync.Mutex
}

// errStop means the campaign is no longer generating at the job's version
// (cancelled, or a newer generation started): the job stops and writes
// nothing more.
var errStop = errors.New("campaign: generation no longer current")

// Run runs one generation. A failed generation is an outcome, written to
// the campaign; Run returns an error only when the job itself could not do
// its work (a database error, or the context ended while the campaign was
// still generating), so River can retry it.
func (g *Generator) Run(ctx context.Context, args jobs.GenerateArgs) error {
	r := &genRun{g: g, id: args.CampaignID, version: args.Version}
	err := r.run(ctx)
	if errors.Is(err, errStop) {
		return nil
	}
	if err != nil && ctx.Err() != nil {
		// River cancelled the job, or the worker is shutting down. If the
		// campaign moved on, there is nothing left to do.
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
}

func (r *genRun) log() *slog.Logger {
	if r.g.Log != nil {
		return r.g.Log
	}
	return slog.New(slog.DiscardHandler)
}

// current reports whether the campaign is still generating at the version.
func (r *genRun) current(ctx context.Context) (bool, error) {
	return store.LockGenerating(ctx, r.g.Pool, r.id, r.version)
}

// write runs fn in a transaction that holds the campaign row, only while the
// generation is current. Otherwise it returns errStop and writes nothing.
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

	// Segment stage.
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

	// Content stage, streamed.
	var cont content.Content
	var contWarnings []validation.Issue
	out, err = r.stage(ctx, llm.NewConversation(llm.Content, llm.ContentTurn(c.Brief, description)), func(text string) validation.Result {
		var res validation.Result
		cont, res = content.ValidateJSON(r.g.Schemas, []byte(text))
		contWarnings = res.Warnings
		return res
	}, r.newDeltas)
	if err != nil || out != nil {
		return r.finish(ctx, err, out)
	}

	// Success: the revision, the transition and the done event together.
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
	return r.write(ctx, func(tx pgx.Tx) error {
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
	})
}

// failure is how a stage failed: a reason and the errors to show.
type failure struct {
	stage  llm.Stage
	reason string
	errors []validation.Issue
}

// finish turns a stage result into the job's result: a failure moves the
// campaign to generation_failed with a failed event.
func (r *genRun) finish(ctx context.Context, err error, f *failure) error {
	if err != nil {
		return err
	}
	errs := f.errors
	if errs == nil {
		errs = []validation.Issue{}
	}
	r.log().Warn("generation failed", "campaign_id", r.id, "stage", f.stage, "reason", f.reason, "errors", len(errs))
	return r.write(ctx, func(tx pgx.Tx) error {
		if err := Transition(ctx, tx, Move{ID: r.id, From: Generating, To: GenerationFailed, Version: r.version,
			Actor: WorkerActor, Reason: f.reason}); err != nil {
			return err
		}
		_, err := store.InsertGenerationEvent(ctx, tx, r.id, "failed", map[string]any{"reason": f.reason, "stage": f.stage, "errors": errs})
		return err
	})
}

// stage runs one stage: up to MaxValidationAttempts answers, each after up
// to MaxTransportRetries transport retries. validate checks an answer and
// keeps what it needs. deltas, if set, streams the answer's copy fields. It
// returns a failure, or nil when an answer was valid.
func (r *genRun) stage(ctx context.Context, conv *llm.Conversation, validate func(string) validation.Result,
	deltas func(ctx context.Context, attempt int) *deltaWriter) (*failure, error) {
	stage := conv.Stage
	var last []validation.Issue
	for attempt := 1; attempt <= MaxValidationAttempts; attempt++ {
		var resp llm.Response
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
				return nil, err // the job's context ended
			}
			if werr := r.record(ctx, store.LLMCall{Stage: string(stage), Attempt: attempt, Outcome: "transport_error",
				ErrorClass: string(ce.Class), Model: r.g.LLM.Model(), LatencyMS: int(time.Since(start).Milliseconds())}); werr != nil {
				return nil, werr
			}
			if dw != nil && dw.wrote {
				// Copy was shown for a call that broke off: tell the reader
				// to clear it.
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
		var issues []validation.Issue
		switch resp.StopReason {
		case "refusal":
			row.Outcome = "refusal"
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
				return nil, r.record(ctx, row)
			}
			row.Outcome = "invalid"
			issues = res.Errors
		}
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

// deltaWriter turns streamed answer text into delta events: the new text of
// each copy field, at most once per DeltaInterval, and the rest on flush.
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

// GenerateWorker runs generate jobs.
type GenerateWorker struct {
	river.WorkerDefaults[jobs.GenerateArgs]
	g *Generator
}

// NewGenerateWorker wraps a generator as a River worker.
func NewGenerateWorker(g *Generator) *GenerateWorker { return &GenerateWorker{g: g} }

// Timeout bounds one generation (both stages with all retries).
func (w *GenerateWorker) Timeout(*river.Job[jobs.GenerateArgs]) time.Duration {
	return jobs.GenerateTimeout
}

func (w *GenerateWorker) Work(ctx context.Context, job *river.Job[jobs.GenerateArgs]) error {
	return w.g.Run(ctx, job.Args)
}

// Inserter inserts a River job in a transaction. *river.Client[pgx.Tx]
// satisfies it.
type Inserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// StartGeneration moves a campaign to generating, writes its started event
// and enqueues the generate job, in one transaction: none of them exists
// without the others. It returns the job's arguments.
func StartGeneration(ctx context.Context, db Beginner, ins Inserter, id string, version int, actor string) (jobs.GenerateArgs, error) {
	args, _, err := BeginGeneration(ctx, db, ins, id, version, actor)
	return args, err
}

// BeginGeneration is StartGeneration that also returns the id of the
// started event, so a reader can stream this generation's events only.
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

// JobCanceller cancels a River job in a transaction. *river.Client[pgx.Tx]
// satisfies it.
type JobCanceller interface {
	JobCancelTx(ctx context.Context, tx pgx.Tx, jobID int64) (*rivertype.JobRow, error)
}

// CancelWithJobs is Cancel that also cancels the campaign's unfinished
// generate jobs through River, in the same transaction. A running job gets
// its context cancelled; a queued one never starts.
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
