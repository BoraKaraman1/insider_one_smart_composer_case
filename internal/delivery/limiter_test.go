package delivery

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestLimiterSettings(t *testing.T) {
	l := NewLimiter()
	if l.Limit() != RatePerSecond || l.Burst() != Burst {
		t.Errorf("limiter %v/s burst %d; want %d/s burst %d", l.Limit(), l.Burst(), RatePerSecond, Burst)
	}
	if RatePerSecond != 100 || Burst != 1 {
		t.Errorf("rule 8 is 100 per second with burst 1, got %d and %d", RatePerSecond, Burst)
	}
}

// The first check: x/time/rate must run on the bubble's fake clock, or the
// big test below measures nothing. Two tokens are 10 ms apart, exactly.
func TestLimiterRunsOnTheFakeClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLimiter()
		ctx := t.Context()
		start := time.Now()
		if err := l.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		if err := l.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		if got := time.Since(start); got != 10*time.Millisecond {
			t.Fatalf("second token after %v of fake time, want exactly 10ms", got)
		}
	})
}

// Rule 8: 500 goroutines hammer one limiter for 10 virtual seconds. No
// half-open 1 s window may hold more than 100 calls, and the total must be
// close to 1,000. With burst 100 a window could hold close to 200.
func TestLimiterNeverExceeds100PerSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLimiter()
		start := time.Now()
		end := start.Add(10 * time.Second)
		ctx, cancel := context.WithDeadline(t.Context(), end)
		defer cancel()

		var mu sync.Mutex
		var calls []time.Time
		var wg sync.WaitGroup
		for range 500 {
			wg.Go(func() {
				for {
					if err := l.Wait(ctx); err != nil {
						return
					}
					now := time.Now()
					if !now.Before(end) {
						return
					}
					// x/time/rate computes in float64 seconds, so a token can
					// come 1 ns early (2.009999999 s instead of 2.01 s). That
					// would put 101 calls in [1.01 s, 2.01 s). Rounding to the
					// microsecond drops that drift and nothing else.
					mu.Lock()
					calls = append(calls, now.Round(time.Microsecond))
					mu.Unlock()
				}
			})
		}
		wg.Wait()

		if n := len(calls); n < 990 || n > 1001 {
			t.Errorf("%d calls in 10 s, want 990 to 1,001", n)
		}
		if w := maxInWindow(calls, time.Second); w > 100 {
			t.Errorf("a 1 s window holds %d calls, want at most 100", w)
		}
	})
}

// maxInWindow is the most calls in any half-open window [t, t+d). The
// densest window always starts at a call.
func maxInWindow(times []time.Time, d time.Duration) int {
	ts := slices.Clone(times)
	slices.SortFunc(ts, func(a, b time.Time) int { return a.Compare(b) })
	best, j := 0, 0
	for i := range ts {
		for j < len(ts) && ts[j].Before(ts[i].Add(d)) {
			j++
		}
		best = max(best, j-i)
	}
	return best
}

func TestMaxInWindow(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(ms ...int) []time.Time {
		var out []time.Time
		for _, m := range ms {
			out = append(out, base.Add(time.Duration(m)*time.Millisecond))
		}
		return out
	}
	cases := []struct {
		times []time.Time
		want  int
	}{
		{nil, 0},
		{at(0), 1},
		{at(0, 999), 2},
		{at(0, 1000), 1}, // half-open: 1000 ms is the next window
		{at(500, 0, 999, 1400), 3},
	}
	for _, c := range cases {
		if got := maxInWindow(c.times, time.Second); got != c.want {
			t.Errorf("maxInWindow(%v) = %d, want %d", c.times, got, c.want)
		}
	}
}

// Each channel has its own limiter: a busy SMS queue does not slow email.
func TestLimitersArePerChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ls := NewLimiters()
		if len(ls) != 2 || ls["sms"] == nil || ls["email"] == nil {
			t.Fatalf("limiters %v; want sms and email", ls)
		}
		ctx := t.Context()
		start := time.Now()
		for _, ch := range []string{"sms", "email"} {
			if err := ls[ch].Wait(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if got := time.Since(start); got != 0 {
			t.Errorf("the first email token waited %v behind SMS", got)
		}
	})
}
