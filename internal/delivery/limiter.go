// Package delivery sends approved campaigns: the fan-out job that creates
// messages and send jobs, the send workers, the per-channel rate limiter
// and the provider client.
package delivery

import (
	"context"

	"golang.org/x/time/rate"
)

// Rule 8: 100 messages per second per channel. Every provider call takes a
// token, retries included. With burst 1, no 1 s window holds more than 100
// calls; a burst of 100 could let close to 200 through.
const (
	RatePerSecond = 100
	Burst         = 1
)

// Channels are the delivery channels, which are also the send queue names.
var Channels = []string{"sms", "email"}

// NewLimiter returns one channel's limiter.
func NewLimiter() *rate.Limiter { return rate.NewLimiter(RatePerSecond, Burst) }

// Limiter hands out one token per provider call. *rate.Limiter satisfies
// it.
type Limiter interface {
	Wait(ctx context.Context) error
}

// NewLimiters returns a limiter per channel, so a busy SMS queue never slows
// email.
func NewLimiters() map[string]Limiter {
	out := make(map[string]Limiter, len(Channels))
	for _, ch := range Channels {
		out[ch] = NewLimiter()
	}
	return out
}
