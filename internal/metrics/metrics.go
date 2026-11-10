// Paket süreç sayaçlarını ve kazıma anındaki göstergeleri tutar.
package metrics

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Series struct {
	Name   string
	Kind   string
	Labels []string
}

type entry struct {
	Series
	help    string
	buckets []float64
	db      bool
}

var (
	llmBuckets        = prometheus.ExponentialBuckets(0.25, 2, 10)
	generationBuckets = prometheus.ExponentialBuckets(1, 2, 10)
)

var catalog = []entry{
	{Series: Series{"messages_total", "counter", []string{"channel", "outcome"}},
		help: "Messages that reached an outcome (sent, failed, dead, skipped). Counted only when the guarded update applied."},
	{Series: Series{"provider_requests_total", "counter", []string{"channel", "class"}},
		help: "Provider calls by result class, retries included."},
	{Series: Series{"provider_request_duration_seconds", "histogram", []string{"channel"}},
		help: "Latency of one provider call."},
	{Series: Series{"retries_total", "counter", []string{"channel", "reason"}},
		help: "Messages put back in the queue for a retry, by error class. Counted only when the guarded update applied."},
	{Series: Series{"rate_limiter_wait_seconds", "histogram", []string{"channel"}},
		help: "Time spent waiting for a rate limiter token."},
	{Series: Series{"llm_requests_total", "counter", []string{"stage", "outcome"}},
		help: "LLM calls by stage and outcome (valid, invalid, refusal, max_tokens, transport_error)."},
	{Series: Series{"llm_request_duration_seconds", "histogram", []string{"stage"}},
		help: "Latency of one LLM call.", buckets: llmBuckets},
	{Series: Series{"generation_duration_seconds", "histogram", []string{"outcome"}},
		help: "Wall clock of one generation (done or failed), retries and backoff included.", buckets: generationBuckets},
	{Series: Series{"llm_tokens_total", "counter", []string{"stage", "direction"}},
		help: "LLM tokens by direction (input, output). Tokens, not cost."},
	{Series: Series{"llm_validation_failures_total", "counter", []string{"stage", "rule"}},
		help: "Validation codes of rejected LLM answers, once per code per answer."},
	{Series: Series{"worker_last_processed_timestamp_seconds", "gauge", nil},
		help: "Unix time this worker last finished a job. 0 means none yet."},
	{Series: Series{"messages", "gauge", []string{"channel", "state"}},
		help: "Messages of all campaigns by channel and state, read from the database at scrape time.", db: true},
	{Series: Series{"queue_jobs", "gauge", []string{"queue", "state"}},
		help: "River jobs by queue and state, read from the database at scrape time.", db: true},
	{Series: Series{"queue_oldest_job_age_seconds", "gauge", []string{"queue"}},
		help: "Age since creation of the oldest available job per queue, read from the database at scrape time. 0 when none waits.", db: true},
	{Series: Series{"http_requests_total", "counter", []string{"route", "status"}},
		help: "API requests by mux pattern and status."},
	{Series: Series{"sse_streams", "gauge", nil},
		help: "Open generation event streams."},
}

func Catalog() []Series {
	out := make([]Series, len(catalog))
	for i, e := range catalog {
		out[i] = e.Series
	}
	return out
}

type Registry struct {
	reg        *prometheus.Registry
	counters   map[string]*prometheus.CounterVec
	histograms map[string]*prometheus.HistogramVec
	gauges     map[string]*prometheus.GaugeVec
}

// Etiketsiz seri ilk yazılana kadar görünmez.
func New() *Registry {
	r := &Registry{
		reg:        prometheus.NewRegistry(),
		counters:   map[string]*prometheus.CounterVec{},
		histograms: map[string]*prometheus.HistogramVec{},
		gauges:     map[string]*prometheus.GaugeVec{},
	}
	r.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	for _, e := range catalog {
		if e.db {
			continue
		}
		switch e.Kind {
		case "counter":
			v := prometheus.NewCounterVec(prometheus.CounterOpts{Name: e.Name, Help: e.help}, e.Labels)
			r.counters[e.Name] = v
			r.reg.MustRegister(v)
		case "histogram":
			v := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: e.Name, Help: e.help, Buckets: e.buckets}, e.Labels)
			r.histograms[e.Name] = v
			r.reg.MustRegister(v)
		case "gauge":
			v := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: e.Name, Help: e.help}, e.Labels)
			r.gauges[e.Name] = v
			r.reg.MustRegister(v)
		}
	}
	return r
}

func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
		ErrorLog:      slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	})
}

func (r *Registry) ProviderRequest(channel, class string, d time.Duration) {
	r.counters["provider_requests_total"].WithLabelValues(channel, class).Inc()
	r.histograms["provider_request_duration_seconds"].WithLabelValues(channel).Observe(d.Seconds())
}

func (r *Registry) LimiterWait(channel string, d time.Duration) {
	r.histograms["rate_limiter_wait_seconds"].WithLabelValues(channel).Observe(d.Seconds())
}

func (r *Registry) Message(channel, outcome string) {
	r.counters["messages_total"].WithLabelValues(channel, outcome).Inc()
}

func (r *Registry) Retry(channel, reason string) {
	r.counters["retries_total"].WithLabelValues(channel, reason).Inc()
}

// Sıfır yazılır ki henüz iş bitirmemiş süreç de seriyi göstersin.
func (r *Registry) WorkerStarted() {
	r.gauges["worker_last_processed_timestamp_seconds"].WithLabelValues().Add(0)
}

func (r *Registry) JobProcessed(at time.Time) {
	r.gauges["worker_last_processed_timestamp_seconds"].WithLabelValues().Set(float64(at.UnixNano()) / 1e9)
}

func (r *Registry) LLMRequest(stage, outcome string, d time.Duration) {
	r.counters["llm_requests_total"].WithLabelValues(stage, outcome).Inc()
	r.histograms["llm_request_duration_seconds"].WithLabelValues(stage).Observe(d.Seconds())
}

func (r *Registry) LLMTokens(stage string, input, output int) {
	r.counters["llm_tokens_total"].WithLabelValues(stage, "input").Add(float64(input))
	r.counters["llm_tokens_total"].WithLabelValues(stage, "output").Add(float64(output))
}

func (r *Registry) LLMValidationFailure(stage, rule string) {
	r.counters["llm_validation_failures_total"].WithLabelValues(stage, rule).Inc()
}

func (r *Registry) Generation(outcome string, d time.Duration) {
	r.histograms["generation_duration_seconds"].WithLabelValues(outcome).Observe(d.Seconds())
}

func (r *Registry) HTTPRequest(route string, status int) {
	r.counters["http_requests_total"].WithLabelValues(route, strconv.Itoa(status)).Inc()
}

func (r *Registry) SSEStreams(delta int) {
	r.gauges["sse_streams"].WithLabelValues().Add(float64(delta))
}
