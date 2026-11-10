package delivery

import (
	"context"
	"time"

	"golang.org/x/time/rate"
)

const (
	RatePerSecond = 100
	Burst         = 1 // yığılma bir olunca bir saniyede yüzden fazla çağrı çıkmaz
	// Jetonla çağrı arasında iş parçacığı bekleyebilir. Yüz jeton bir saniye artı bu payda verilir ki
	// bu kadar gecikme bir saniyelik pencereye yüzden fazla çağrı sokmasın.
	Headroom = 50 * time.Millisecond
	Interval = (time.Second + Headroom) / RatePerSecond
)

var Channels = []string{"sms", "email"}

func NewLimiter() *rate.Limiter { return rate.NewLimiter(rate.Every(Interval), Burst) }

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
