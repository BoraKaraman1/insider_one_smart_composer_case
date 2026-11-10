package delivery

import (
	"context"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/metrics/metricstest"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockprovider"
)

func metricsEnv(t *testing.T) (*sendEnv, *metrics.Registry) {
	t.Helper()
	e := newSendEnv(t, mockprovider.Config{})
	reg := metrics.New()
	e.worker.Metrics = reg
	return e, reg
}

type want struct {
	name   string
	labels []string
	delta  float64
}

func checkDeltas(t *testing.T, step string, before, after metricstest.Samples, wants []want) {
	t.Helper()
	for _, w := range wants {
		if d := metricstest.Delta(before, after, w.name, w.labels...); d != w.delta {
			t.Errorf("%s: %s%v moved by %v, want %v", step, w.name, w.labels, d, w.delta)
		}
	}
}

func TestSendMetricsSuccess(t *testing.T) {
	e, reg := metricsEnv(t)
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	before := metricstest.Scrape(t, reg.Handler())
	if err := e.work(id); err != nil {
		t.Fatal(err)
	}
	after := metricstest.Scrape(t, reg.Handler())
	checkDeltas(t, "success", before, after, []want{
		{"messages_total", []string{"channel=sms", "outcome=sent"}, 1},
		{"provider_requests_total", []string{"channel=sms", "class=success"}, 1},
		{"provider_request_duration_seconds_count", []string{"channel=sms"}, 1},
		{"rate_limiter_wait_seconds_count", []string{"channel=sms"}, 1},
		{"retries_total", []string{"channel=sms", "reason=server_error"}, 0},
		{"messages_total", []string{"channel=email", "outcome=sent"}, 0},
	})
	if after.Get("worker_last_processed_timestamp_seconds") <= 0 {
		t.Error("worker_last_processed_timestamp_seconds was not set")
	}
}

func TestSendMetrics503ThenSuccess(t *testing.T) {
	e, reg := metricsEnv(t)
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.prov.force(id, "503", "ok")
	before := metricstest.Scrape(t, reg.Handler())
	if _, ok := snoozed(e.work(id)); !ok {
		t.Fatal("503 was not snoozed")
	}
	mid := metricstest.Scrape(t, reg.Handler())
	checkDeltas(t, "after 503", before, mid, []want{
		{"provider_requests_total", []string{"channel=sms", "class=server_error"}, 1},
		{"retries_total", []string{"channel=sms", "reason=server_error"}, 1},
		{"messages_total", []string{"channel=sms", "outcome=sent"}, 0},
	})
	e.now = e.now.Add(fixedBackoff)
	if err := e.work(id); err != nil {
		t.Fatal(err)
	}
	after := metricstest.Scrape(t, reg.Handler())
	checkDeltas(t, "503 then ok", before, after, []want{
		{"provider_requests_total", []string{"channel=sms", "class=server_error"}, 1},
		{"provider_requests_total", []string{"channel=sms", "class=success"}, 1},
		{"provider_request_duration_seconds_count", []string{"channel=sms"}, 2},
		{"rate_limiter_wait_seconds_count", []string{"channel=sms"}, 2},
		{"retries_total", []string{"channel=sms", "reason=server_error"}, 1},
		{"messages_total", []string{"channel=sms", "outcome=sent"}, 1},
	})
}

func TestSendMetricsPermanent400(t *testing.T) {
	e, reg := metricsEnv(t)
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "email", "")
	e.prov.force(id, "400")
	before := metricstest.Scrape(t, reg.Handler())
	if err := e.work(id); !cancelled(err) {
		t.Fatalf("400: %v, want JobCancel", err)
	}
	after := metricstest.Scrape(t, reg.Handler())
	checkDeltas(t, "400", before, after, []want{
		{"provider_requests_total", []string{"channel=email", "class=client_error"}, 1},
		{"messages_total", []string{"channel=email", "outcome=failed"}, 1},
		{"messages_total", []string{"channel=email", "outcome=sent"}, 0},
		{"retries_total", []string{"channel=email", "reason=client_error"}, 0},
	})
}

func TestSendMetricsDuplicateJob(t *testing.T) {
	e, reg := metricsEnv(t)
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	before := metricstest.Scrape(t, reg.Handler())
	for range 2 {
		if err := e.work(id); err != nil {
			t.Fatal(err)
		}
	}
	after := metricstest.Scrape(t, reg.Handler())
	checkDeltas(t, "duplicate", before, after, []want{
		{"messages_total", []string{"channel=sms", "outcome=sent"}, 1},
		{"provider_requests_total", []string{"channel=sms", "class=success"}, 1},
	})
}

func TestSendMetricsGuardedUpdateNotApplied(t *testing.T) {
	e, reg := metricsEnv(t)
	c := insertSendingCampaign(t, e.pool)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	e.prov.hooks(func(string) {
		if _, err := e.pool.Exec(context.Background(), `UPDATE messages SET state = 'sent', provider_message_id = 'other-run', sent_at = now()
			WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	}, nil)
	before := metricstest.Scrape(t, reg.Handler())
	if err := e.work(id); err != nil && !cancelled(err) {
		t.Fatal(err)
	}
	after := metricstest.Scrape(t, reg.Handler())
	checkDeltas(t, "late run", before, after, []want{
		{"provider_requests_total", []string{"channel=sms", "class=success"}, 1},
		{"messages_total", []string{"channel=sms", "outcome=sent"}, 0},
	})
}

func TestSendMetricsSkip(t *testing.T) {
	e, reg := metricsEnv(t)
	c := insertCampaignIn(t, e.pool, "cancelled", 4)
	id := insertMessage(t, e.pool, c, consentingUsers(t, e.pool, 1)[0], "sms", "")
	before := metricstest.Scrape(t, reg.Handler())
	for range 2 {
		if err := e.work(id); err != nil && !cancelled(err) {
			t.Fatal(err)
		}
	}
	after := metricstest.Scrape(t, reg.Handler())
	checkDeltas(t, "skip", before, after, []want{
		{"messages_total", []string{"channel=sms", "outcome=skipped"}, 1},
		{"provider_requests_total", []string{"channel=sms", "class=success"}, 0},
		{"rate_limiter_wait_seconds_count", []string{"channel=sms"}, 0},
	})
}

func TestSendMetricsNoIDLabels(t *testing.T) {
	e, reg := metricsEnv(t)
	c := insertSendingCampaign(t, e.pool)
	users := consentingUsers(t, e.pool, 2)
	a := insertMessage(t, e.pool, c, users[0], "sms", "")
	b := insertMessage(t, e.pool, c, users[1], "email", "")
	e.prov.force(b, "503")
	_ = e.work(a)
	_ = e.work(b)
	for _, v := range metricstest.Scrape(t, reg.Handler()).LabelValues() {
		if metricstest.IDShaped(v) {
			t.Errorf("label value %q looks like an id", v)
		}
	}
}
