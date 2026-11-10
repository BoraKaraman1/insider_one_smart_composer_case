// Paket onaylı kampanyayı mesajlara böler ve gönderir.
package delivery

import (
	"context"

	"golang.org/x/time/rate"
)

const (
	RatePerSecond = 100
	Burst         = 1 // yığılma bir olunca bir saniyede yüzden fazla çağrı çıkmaz
)

var Channels = []string{"sms", "email"}

func NewLimiter() *rate.Limiter { return rate.NewLimiter(RatePerSecond, Burst) }

type Limiter interface {
	Wait(ctx context.Context) error
}

func NewLimiters() map[string]Limiter {
	out := make(map[string]Limiter, len(Channels))
	for _, ch := range Channels {
		out[ch] = NewLimiter()
	}
	return out
}
