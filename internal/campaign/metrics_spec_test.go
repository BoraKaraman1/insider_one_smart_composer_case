package campaign

import (
	"context"
	"testing"
	"time"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics/metricstest"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockllm"
)

// Plan 3, 3.4: Generator.Metrics is an optional interface that
// *metrics.Registry satisfies; nil is a no-op. Outcome label values:
// llm_requests_total uses the llm_calls outcomes (valid, invalid, refusal,
// max_tokens, transport_error); generation_duration_seconds uses done or
// failed.

const realBackoff = 30 * time.Millisecond

// generation_duration_seconds is observed once per Run and covers the
// whole run: it is at least the sum of the call latencies plus the backoff.
func TestGenerationMetricsWithBackoff(t *testing.T) {
	p, meta := genPool(t)
	g, m, _ := newGen(t, p, meta.AsOf, mockllm.Options{})
	reg := metrics.New()
	g.Metrics = reg
	// A real, short sleep, so the backoff shows in wall-clock time.
	g.Sleep = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-time.After(realBackoff):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.Script(mockllm.Content, mockllm.Status(529), mockllm.Reply{Text: goodCopy})
	id := insertGenerating(t, p, exampleBrief, 0)

	before := metricstest.Scrape(t, reg.Handler())
	run(t, g, id, 0)
	after := metricstest.Scrape(t, reg.Handler())

	d := func(name string, labels ...string) float64 { return metricstest.Delta(before, after, name, labels...) }
	if n := d("generation_duration_seconds_count", "outcome=done"); n != 1 {
		t.Errorf("generation_duration_seconds{done} observed %v times, want 1", n)
	}
	if n := d("generation_duration_seconds_count", "outcome=failed"); n != 0 {
		t.Errorf("generation_duration_seconds{failed} observed %v times", n)
	}
	for _, w := range []struct {
		stage, outcome string
		n              float64
	}{
		{"segment", "valid", 1},
		{"content", "transport_error", 1},
		{"content", "valid", 1},
		{"content", "invalid", 0},
	} {
		if got := d("llm_requests_total", "stage="+w.stage, "outcome="+w.outcome); got != w.n {
			t.Errorf("llm_requests_total{%s,%s} = %v, want %v", w.stage, w.outcome, got, w.n)
		}
	}
	if n := d("llm_request_duration_seconds_count", "stage=content"); n != 2 {
		t.Errorf("content call latencies observed %v times, want 2", n)
	}
	calls := d("llm_request_duration_seconds_sum", "stage=segment") + d("llm_request_duration_seconds_sum", "stage=content")
	total := d("generation_duration_seconds_sum", "outcome=done")
	if total < calls+realBackoff.Seconds() {
		t.Errorf("generation took %.3fs, less than its calls (%.3fs) plus the backoff (%.3fs)", total, calls, realBackoff.Seconds())
	}
	for _, dir := range []string{"input", "output"} {
		if d("llm_tokens_total", "stage=segment", "direction="+dir) <= 0 {
			t.Errorf("llm_tokens_total{segment,%s} did not move", dir)
		}
	}
}

// Each validation code of a rejected answer counts once under its stage.
func TestGenerationMetricsValidationFailures(t *testing.T) {
	p, meta := genPool(t)
	g, m, _ := newGen(t, p, meta.AsOf, mockllm.Options{})
	reg := metrics.New()
	g.Metrics = reg
	bad := copyJSON("Merhaba {{password}} {{campaign_url}}", "Sepet", "Merhaba {{first_name}} {{campaign_url}}")
	m.Script(mockllm.Content, mockllm.Reply{Text: bad}, mockllm.Reply{Text: goodCopy})
	id := insertGenerating(t, p, exampleBrief, 0)

	before := metricstest.Scrape(t, reg.Handler())
	run(t, g, id, 0)
	after := metricstest.Scrape(t, reg.Handler())

	if n := metricstest.Delta(before, after, "llm_validation_failures_total", "stage=content", "rule=unknown_variable"); n != 1 {
		t.Errorf("llm_validation_failures_total{content,unknown_variable} = %v, want 1", n)
	}
	if n := metricstest.Delta(before, after, "llm_requests_total", "stage=content", "outcome=invalid"); n != 1 {
		t.Errorf("llm_requests_total{content,invalid} = %v, want 1", n)
	}
}

// A refused generation is observed once, as failed.
func TestGenerationMetricsFailed(t *testing.T) {
	p, meta := genPool(t)
	g, m, _ := newGen(t, p, meta.AsOf, mockllm.Options{})
	reg := metrics.New()
	g.Metrics = reg
	m.Script(mockllm.Segment, mockllm.Refusal())
	id := insertGenerating(t, p, exampleBrief, 0)
	before := metricstest.Scrape(t, reg.Handler())
	run(t, g, id, 0)
	after := metricstest.Scrape(t, reg.Handler())
	if n := metricstest.Delta(before, after, "generation_duration_seconds_count", "outcome=failed"); n != 1 {
		t.Errorf("generation_duration_seconds{failed} observed %v times, want 1", n)
	}
	if n := metricstest.Delta(before, after, "llm_requests_total", "stage=segment", "outcome=refusal"); n != 1 {
		t.Errorf("llm_requests_total{segment,refusal} = %v, want 1", n)
	}
}
