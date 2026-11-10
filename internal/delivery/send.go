package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/riverqueue/river"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/retry"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

// Send limits (rule 6).
const (
	ProviderTimeout = 5 * time.Second // per attempt
	MaxAttempts     = 6               // then the message is dead
	MaxAge          = 15 * time.Minute
)

// SendWorker sends one message per job, on the sms and email queues.
type SendWorker struct {
	river.WorkerDefaults[jobs.SendArgs]
	DB              DB
	ProviderURL     string
	Limiters        map[string]Limiter // one per channel; every attempt takes a token
	ProviderTimeout time.Duration
	Now             func() time.Time
	// Backoff gives the delay before retry number n (0 is the first retry),
	// when the provider sent no Retry-After.
	Backoff func(retry int) time.Duration
	Client  *http.Client
	Log     *slog.Logger
	// Metrics counts provider calls and message outcomes. Nil counts
	// nothing.
	Metrics Metrics
}

// Metrics is what the send worker reports. *metrics.Registry satisfies it.
// Message and Retry are called only when the guarded update applied, so a
// duplicate or late run never counts a message twice.
type Metrics interface {
	ProviderRequest(channel, class string, d time.Duration)
	LimiterWait(channel string, d time.Duration)
	Message(channel, outcome string)
	Retry(channel, reason string)
	JobProcessed(at time.Time)
}

type noMetrics struct{}

func (noMetrics) ProviderRequest(string, string, time.Duration) {}
func (noMetrics) LimiterWait(string, time.Duration)             {}
func (noMetrics) Message(string, string)                        {}
func (noMetrics) Retry(string, string)                          {}
func (noMetrics) JobProcessed(time.Time)                        {}

func (w *SendWorker) metrics() Metrics {
	if w.Metrics != nil {
		return w.Metrics
	}
	return noMetrics{}
}

// RandomBackoff is retry.Backoff over a shared, locked random source.
func RandomBackoff(rng *rand.Rand) func(int) time.Duration {
	var mu sync.Mutex
	return func(n int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return retry.Backoff(n, rng)
	}
}

// Timeout bounds one job run.
func (w *SendWorker) Timeout(*river.Job[jobs.SendArgs]) time.Duration { return jobs.SendTimeout }

func (w *SendWorker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Work sends a message at least once. Every state update is guarded by its
// predecessors. Retries carry the message id as the idempotency key, so the
// provider sends once however often this runs. A retryable failure snoozes
// the job; a permanent one fails the message and cancels the job; a
// database error is returned, so River runs the job again and the message,
// still in sending, is reclaimed.
func (w *SendWorker) Work(ctx context.Context, job *river.Job[jobs.SendArgs]) error {
	id := job.Args.MessageID
	log := w.log().With("message_id", id)
	met := w.metrics()
	defer func() { met.JobProcessed(time.Now()) }()
	m, err := store.LoadSend(ctx, w.DB, id)
	if errors.Is(err, store.ErrNotFound) {
		return river.JobCancel(fmt.Errorf("send: message %s does not exist", id))
	}
	if err != nil {
		return err
	}
	if done, err := w.gate(ctx, log, m); done {
		return err
	}

	lim := w.Limiters[m.Channel]
	if lim == nil {
		return fmt.Errorf("send: no limiter for channel %q", m.Channel)
	}
	waitStart := time.Now()
	if err := lim.Wait(ctx); err != nil {
		return err
	}
	met.LimiterWait(m.Channel, time.Since(waitStart))
	// The claim is the dispatch cutoff. It checks consent and cancellation
	// again, because either may have changed while we waited for the token.
	now := w.Now()
	attempts, first, ok, err := store.ClaimMessage(ctx, w.DB, id, now)
	if err != nil {
		return err
	}
	if !ok {
		if m, err = store.LoadSend(ctx, w.DB, id); err != nil {
			return err
		}
		if done, err := w.gate(ctx, log, m); done {
			return err
		}
		// Consent came back between the claim and this read: run again.
		return fmt.Errorf("send: claim of %s did not apply", id)
	}
	payload, err := w.payload(m)
	if err != nil {
		// A template the validator let through but the renderer rejects.
		ok, uerr := store.MarkFinal(ctx, w.DB, id, "failed", string(retry.ClientError), "render: "+err.Error())
		if uerr != nil {
			return uerr
		}
		if ok {
			met.Message(m.Channel, "failed")
		}
		return w.finish(ctx, log, m, river.JobCancel(err))
	}
	callStart := time.Now()
	providerID, res, lastErr := w.post(ctx, id, payload)
	met.ProviderRequest(m.Channel, string(res.Class), time.Since(callStart))
	now = w.Now()
	switch {
	case res.Class == retry.Success:
		ok, err := store.MarkSent(ctx, w.DB, id, providerID, now)
		if err != nil {
			return err
		}
		if ok {
			met.Message(m.Channel, "sent")
		}
		log.Info("message sent", "channel", m.Channel, "attempts", attempts)
		return w.finish(ctx, log, m, nil)

	case !res.Class.Retryable():
		ok, err := store.MarkFinal(ctx, w.DB, id, "failed", string(res.Class), lastErr)
		if err != nil {
			return err
		}
		if ok {
			met.Message(m.Channel, "failed")
		}
		log.Warn("message failed", "channel", m.Channel, "class", res.Class, "attempts", attempts)
		return w.finish(ctx, log, m, river.JobCancel(fmt.Errorf("send: %s", lastErr)))

	case attempts >= MaxAttempts || now.Sub(first) >= MaxAge:
		ok, err := store.MarkFinal(ctx, w.DB, id, "dead", string(res.Class), lastErr)
		if err != nil {
			return err
		}
		if ok {
			met.Message(m.Channel, "dead")
		}
		log.Warn("message dead", "channel", m.Channel, "class", res.Class, "attempts", attempts)
		return w.finish(ctx, log, m, nil)
	}

	delay := res.RetryAfter
	if delay == 0 {
		delay = w.Backoff(attempts - 1)
	}
	ok, err = store.MarkRetry(ctx, w.DB, id, string(res.Class), lastErr, now.Add(delay))
	if err != nil {
		return err
	}
	if ok {
		met.Retry(m.Channel, string(res.Class))
	}
	log.Info("message retry", "channel", m.Channel, "class", res.Class, "attempts", attempts, "delay_ms", delay.Milliseconds())
	return river.JobSnooze(delay)
}

// gate decides whether a message may still be sent. It reports done for a
// terminal message, which only gets the completion check again (an earlier
// run may have failed it after the terminal update), and for a cancelled
// campaign or a lost consent, which skip the message.
func (w *SendWorker) gate(ctx context.Context, log *slog.Logger, m store.Send) (bool, error) {
	switch {
	case m.State != "queued" && m.State != "sending":
		return true, w.finish(ctx, log, m, nil) // terminal: never sent again
	case campaign.State(m.CampaignState) == campaign.Cancelled:
		return true, w.skip(ctx, log, m, "cancelled")
	case !m.Consent:
		return true, w.skip(ctx, log, m, "no_consent")
	}
	return false, nil
}

func (w *SendWorker) skip(ctx context.Context, log *slog.Logger, m store.Send, class string) error {
	ok, err := store.SkipMessage(ctx, w.DB, m.ID, class)
	if err != nil {
		return err
	}
	if ok {
		w.metrics().Message(m.Channel, "skipped")
	}
	log.Info("message skipped", "channel", m.Channel, "reason", class)
	return w.finish(ctx, log, m, nil)
}

// finish runs the completion check after a terminal update and returns ret.
func (w *SendWorker) finish(ctx context.Context, log *slog.Logger, m store.Send, ret error) error {
	if err := campaign.CompleteIfDone(ctx, w.DB, m.CampaignID); err != nil {
		log.Error("completion check failed", "campaign_id", m.CampaignID, "error", err)
		return err
	}
	return ret
}

type providerMessage struct {
	Channel string `json:"channel"`
	To      string `json:"to"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

// payload renders the message. The renderer gets a RenderContext, never the
// users row; contact data goes only to "to".
func (w *SendWorker) payload(m store.Send) ([]byte, error) {
	rc := content.RenderContext{FirstName: m.FirstName, LastName: m.LastName, City: m.City, CampaignURL: m.CampaignURL}
	pm := providerMessage{Channel: m.Channel}
	var err error
	if m.Channel == "sms" {
		pm.To = m.Phone
		pm.Body, err = content.RenderText(m.SMSBody, rc)
	} else {
		pm.To = m.Email
		if pm.Subject, err = content.RenderText(m.EmailSubject, rc); err == nil {
			pm.Body, err = content.RenderEmailHTML(m.EmailBody, rc)
		}
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(pm)
}

// post makes one provider call with the message id as the idempotency key.
// lastErr describes a failure without any message content.
func (w *SendWorker) post(ctx context.Context, id string, payload []byte) (providerID string, res retry.Result, lastErr string) {
	timeout := w.ProviderTimeout
	if timeout <= 0 {
		timeout = ProviderTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(w.ProviderURL, "/")+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", retry.Result{Class: retry.ClientError}, "request: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", id)
	// net/http treats a request with an Idempotency-Key as replayable and
	// silently resends it on a dropped connection. That call would take no
	// limiter token and count no attempt. Without GetBody the transport
	// cannot replay it, so every retry goes through Work.
	req.GetBody = nil
	client := w.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		res := retry.Classify(nil, err, time.Now())
		return "", res, fmt.Sprintf("provider call failed: %s", res.Class)
	}
	defer resp.Body.Close()
	// The status decides first: a broken body must not turn a permanent 4xx
	// into a retryable read error. Only a success needs its body.
	res = retry.Classify(resp, nil, time.Now())
	if res.Class != retry.Success {
		return "", res, fmt.Sprintf("provider answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		res := retry.Classify(nil, err, time.Now())
		return "", res, fmt.Sprintf("reading the provider answer failed: %s", res.Class)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" {
		// Sent, but we cannot tell the id: retry, the replay will carry it.
		return "", retry.Result{Class: retry.ServerError}, "provider answered without an id"
	}
	return out.ID, res, ""
}
