// Package jobs holds the River job argument types, queue names and worker
// settings. It is the contract between the side that inserts jobs (api,
// fan-out) and the workers, so both can be built in parallel. Kinds and
// queue names are stored in river_job rows: never rename one.
package jobs

import (
	"log/slog"
	"time"

	"github.com/riverqueue/river"
)

// Queue names.
const (
	QueueGenerate = "generate"
	QueueFanout   = "fanout"
	QueueSMS      = "sms"
	QueueEmail    = "email"
)

// QueueWorkers is how many jobs each queue runs at once in one worker process.
var QueueWorkers = map[string]int{
	QueueGenerate: 2,
	QueueFanout:   1,
	QueueSMS:      20,
	QueueEmail:    20,
}

// Per-worker job timeouts.
const (
	GenerateTimeout = 6 * time.Minute
	SendTimeout     = 30 * time.Second
)

// RescueStuckJobsAfter must exceed every job timeout. Otherwise the rescuer
// starts a second copy of a job that is still running.
const RescueStuckJobsAfter = 10 * time.Minute

// GenerateArgs asks for one generation of a campaign at a given version. A
// job whose version no longer matches the campaign does nothing.
type GenerateArgs struct {
	CampaignID string `json:"campaign_id"`
	Version    int    `json:"version"`
}

func (GenerateArgs) Kind() string { return "generate" }

func (GenerateArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueGenerate, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// FanoutArgs creates the messages and send jobs of an approved campaign.
type FanoutArgs struct {
	CampaignID string `json:"campaign_id"`
}

func (FanoutArgs) Kind() string { return "fanout" }

func (FanoutArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueFanout, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// SendArgs sends one message. It has no default queue: the inserter sets
// Queue to the message's channel (QueueSMS or QueueEmail), so each channel
// has its own workers.
type SendArgs struct {
	MessageID string `json:"message_id"`
}

func (SendArgs) Kind() string { return "send" }

func (SendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// ClientConfig is the River configuration of a worker process: every queue
// with its worker count, and the stuck-job rescue delay. Job timeouts are
// set per worker (GenerateTimeout, SendTimeout). An insert-only client (the
// api) needs no config beyond a driver.
func ClientConfig(workers *river.Workers, log *slog.Logger) *river.Config {
	queues := make(map[string]river.QueueConfig, len(QueueWorkers))
	for q, n := range QueueWorkers {
		queues[q] = river.QueueConfig{MaxWorkers: n}
	}
	return &river.Config{
		Queues:               queues,
		Workers:              workers,
		RescueStuckJobsAfter: RescueStuckJobsAfter,
		Logger:               log,
	}
}
