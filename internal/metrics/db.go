package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

// scrapeTimeout bounds the database queries of one scrape.
const scrapeTimeout = 2 * time.Second

// channels and queues are the closed sets the gauges always expose, at 0
// when the database has no row for them.
var (
	channels = []string{"sms", "email"}
	queues   = []string{jobs.QueueGenerate, jobs.QueueFanout, jobs.QueueSMS, jobs.QueueEmail}
)

// WatchDatabase registers the scrape-time gauges: messages, queue_jobs and
// queue_oldest_job_age_seconds. They read db on every scrape, under one
// 2 s timeout.
func (r *Registry) WatchDatabase(db store.DB) {
	c := &dbCollector{db: db, descs: map[string]*prometheus.Desc{}}
	for _, e := range catalog {
		if e.db {
			c.descs[e.Name] = prometheus.NewDesc(e.Name, e.help, e.Labels, nil)
		}
	}
	r.reg.MustRegister(c)
}

type dbCollector struct {
	db    store.DB
	descs map[string]*prometheus.Desc
}

func (c *dbCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range c.descs {
		ch <- d
	}
}

// Collect reads the queue first: under a slow messages table the queue
// gauges, the ones to alert on, still make it.
func (c *dbCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()
	gauge := func(name string, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(c.descs[name], prometheus.GaugeValue, v, labels...)
	}

	jobCounts, oldest, err := store.QueueGauge(ctx, c.db)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.descs["queue_jobs"], err)
	} else {
		for k, n := range jobCounts {
			gauge("queue_jobs", float64(n), k[0], k[1])
		}
		for _, q := range queues {
			gauge("queue_oldest_job_age_seconds", oldest[q], q)
		}
	}

	msgs, err := store.MessageGauge(ctx, c.db)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.descs["messages"], err)
		return
	}
	for _, chn := range channels {
		for _, s := range store.MessageStates {
			gauge("messages", float64(msgs[[2]string{chn, s}]), chn, s)
		}
	}
}
