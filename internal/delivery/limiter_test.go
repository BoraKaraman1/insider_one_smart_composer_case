package delivery

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/time/rate"
)

func TestLimiterSettings(t *testing.T) {
	l := NewLimiter()
	if l.Limit() != rate.Every(Interval) || l.Burst() != Burst {
		t.Errorf("limiter %v/s burst %d; want one token every %v, burst %d", l.Limit(), l.Burst(), Interval, Burst)
	}
	if Interval != 10500*time.Microsecond {
		t.Errorf("interval %v; want 10.5ms, 100 tokens per 1.05 s", Interval)
	}
	if RatePerSecond != 100 || Burst != 1 {
		t.Errorf("rule 8 is 100 per second with burst 1, got %d and %d", RatePerSecond, Burst)
	}
}

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
		if got := time.Since(start); got != Interval {
			t.Fatalf("second token after %v of fake time, want exactly %v", got, Interval)
		}
	})
}

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
					mu.Lock()
					// Kayan nokta bir nanosaniye erken jeton verebiliyor, mikrosaniyeye yuvarlayınca kayboluyor.
					calls = append(calls, now.Round(time.Microsecond))
					mu.Unlock()
				}
			})
		}
		wg.Wait()

		// On saniyede 10.5 ms arayla 953 jeton çıkar.
		if n := len(calls); n < 945 || n > 953 {
			t.Errorf("%d calls in 10 s, want 945 to 953", n)
		}
		// Pay kadar geciken çağrı bile bir saniyelik pencereye yüzden fazla çağrı sokamaz.
		if w := maxInWindow(calls, time.Second+Headroom); w > RatePerSecond {
			t.Errorf("a %v window holds %d calls, want at most %d", time.Second+Headroom, w, RatePerSecond)
		}
	})
}

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
		{at(0, 1000), 1},
		{at(500, 0, 999, 1400), 3},
	}
	for _, c := range cases {
		if got := maxInWindow(c.times, time.Second); got != c.want {
			t.Errorf("maxInWindow(%v) = %d, want %d", c.times, got, c.want)
		}
	}
}

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
