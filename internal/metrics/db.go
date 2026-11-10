package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

const scrapeTimeout = 2 * time.Second

var (
	channels = []string{"sms", "email"}
	queues   = []string{jobs.QueueGenerate, jobs.QueueFanout, jobs.QueueSMS, jobs.QueueEmail}
)

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

// Kuyruk önce okunur, mesaj tablosu yavaşsa da o göstergeler yetişir.
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
