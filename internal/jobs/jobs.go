// Paket kuyruk işlerinin argümanlarını ve zaman aşımı ayarlarını tutar.
package jobs

import (
	"log/slog"
	"time"

	"github.com/riverqueue/river"
)

const (
	QueueGenerate = "generate"
	QueueFanout   = "fanout"
	QueueSMS      = "sms"
	QueueEmail    = "email"
)

var QueueWorkers = map[string]int{
	QueueGenerate: 2,
	QueueFanout:   1,
	QueueSMS:      20,
	QueueEmail:    20,
}

const (
	GenerateTimeout = 6 * time.Minute
	SendTimeout     = 30 * time.Second
)

const RescueStuckJobsAfter = 10 * time.Minute

type GenerateArgs struct {
	CampaignID string `json:"campaign_id"`
	Version    int    `json:"version"`
}

func (GenerateArgs) Kind() string { return "generate" }

func (GenerateArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueGenerate, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

type FanoutArgs struct {
	CampaignID string `json:"campaign_id"`
}

func (FanoutArgs) Kind() string { return "fanout" }

func (FanoutArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueFanout, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// Varsayılan kuyruk yok, ekleyen kanalı kendi yazar.
type SendArgs struct {
	MessageID string `json:"message_id"`
}

func (SendArgs) Kind() string { return "send" }

func (SendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

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
