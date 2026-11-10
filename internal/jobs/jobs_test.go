package jobs

import (
	"encoding/json"
	"maps"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

// Kinds and queue names are stored in river_job rows. Renaming one strands
// every job already in the table, so the strings are pinned here.
func TestKindsAndQueues(t *testing.T) {
	if got := (GenerateArgs{}).Kind(); got != "generate" {
		t.Errorf("GenerateArgs kind %q", got)
	}
	if got := (FanoutArgs{}).Kind(); got != "fanout" {
		t.Errorf("FanoutArgs kind %q", got)
	}
	if got := (SendArgs{}).Kind(); got != "send" {
		t.Errorf("SendArgs kind %q", got)
	}
	queues := map[string]string{
		"generate": QueueGenerate,
		"fanout":   QueueFanout,
		"sms":      QueueSMS,
		"email":    QueueEmail,
	}
	for want, got := range queues {
		if got != want {
			t.Errorf("queue constant %q, want %q", got, want)
		}
	}
	if got := (GenerateArgs{}).InsertOpts().Queue; got != QueueGenerate {
		t.Errorf("GenerateArgs goes to queue %q", got)
	}
	if got := (FanoutArgs{}).InsertOpts().Queue; got != QueueFanout {
		t.Errorf("FanoutArgs goes to queue %q", got)
	}
}

// Each job is unique by its arguments: one generation per campaign version,
// one fan-out per campaign, one send job per message (rule 10).
func TestJobsAreUniqueByArgs(t *testing.T) {
	if !(GenerateArgs{}).InsertOpts().UniqueOpts.ByArgs {
		t.Error("GenerateArgs is not unique by args")
	}
	if !(FanoutArgs{}).InsertOpts().UniqueOpts.ByArgs {
		t.Error("FanoutArgs is not unique by args")
	}
	if !(SendArgs{}).InsertOpts().UniqueOpts.ByArgs {
		t.Error("SendArgs is not unique by args")
	}
}

// The encoded arguments are the contract between the inserting side (api,
// fan-out) and the workers, which are built in separate sessions.
func TestArgsJSON(t *testing.T) {
	const id = "00000000-0000-4000-8000-0000000000a1"
	cases := []struct {
		args any
		want string
	}{
		{GenerateArgs{CampaignID: id, Version: 3}, `{"campaign_id":"` + id + `","version":3}`},
		{FanoutArgs{CampaignID: id}, `{"campaign_id":"` + id + `"}`},
		{SendArgs{MessageID: id}, `{"message_id":"` + id + `"}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.args)
		if err != nil || string(got) != c.want {
			t.Errorf("%T: %s, %v; want %s", c.args, got, err, c.want)
		}
	}
	// Version 0 is a real value (a campaign that was never transitioned), so
	// it must not be dropped from the encoding.
	got, _ := json.Marshal(GenerateArgs{CampaignID: id})
	if string(got) != `{"campaign_id":"`+id+`","version":0}` {
		t.Errorf("version 0 encodes as %s", got)
	}
}

func TestWorkerSettings(t *testing.T) {
	want := map[string]int{QueueGenerate: 2, QueueFanout: 1, QueueSMS: 20, QueueEmail: 20}
	if !maps.Equal(QueueWorkers, want) {
		t.Errorf("QueueWorkers %v, want %v", QueueWorkers, want)
	}
	if GenerateTimeout != 6*time.Minute {
		t.Errorf("GenerateTimeout %v, want 6m", GenerateTimeout)
	}
	if SendTimeout != 30*time.Second {
		t.Errorf("SendTimeout %v, want 30s", SendTimeout)
	}
	// River's rescuer treats a job running longer than this as stuck and
	// runs it again. Below a job timeout it would start a second copy of a
	// job that is still running.
	for name, timeout := range map[string]time.Duration{"generate": GenerateTimeout, "send": SendTimeout} {
		if RescueStuckJobsAfter <= timeout {
			t.Errorf("RescueStuckJobsAfter %v does not exceed the %s timeout %v", RescueStuckJobsAfter, name, timeout)
		}
	}
}

func TestClientConfig(t *testing.T) {
	workers := river.NewWorkers()
	cfg := ClientConfig(workers, nil)
	if cfg.Workers != workers || cfg.RescueStuckJobsAfter != RescueStuckJobsAfter {
		t.Errorf("config %+v", cfg)
	}
	got := map[string]int{}
	for q, qc := range cfg.Queues {
		got[q] = qc.MaxWorkers
	}
	if !maps.Equal(got, QueueWorkers) {
		t.Errorf("queues %v, want %v", got, QueueWorkers)
	}
}
