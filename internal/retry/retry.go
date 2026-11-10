// Paket çağrı sonucunu sınıflandırır ve bekleme süresini hesaplar.
package retry

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Class string

const (
	Success     Class = "success"
	Timeout     Class = "timeout"
	RateLimited Class = "rate_limited"
	ServerError Class = "server_error"
	ConnRefused Class = "conn_refused"
	ConnReset   Class = "conn_reset"
	ClientError Class = "client_error"
)

func (c Class) Retryable() bool {
	switch c {
	case Timeout, RateLimited, ServerError, ConnRefused, ConnReset:
		return true
	}
	return false
}

const MaxRetryAfter = 60 * time.Second

type Result struct {
	Class      Class
	RetryAfter time.Duration
}

func Classify(resp *http.Response, err error, now time.Time) Result {
	if err != nil {
		return Result{Class: classifyErr(err)}
	}
	switch s := resp.StatusCode; {
	case s >= 200 && s < 300:
		return Result{Class: Success}
	case s == http.StatusRequestTimeout:
		return Result{Class: Timeout}
	case s == http.StatusTooManyRequests:
		return Result{Class: RateLimited, RetryAfter: retryAfter(resp.Header.Get("Retry-After"), now)}
	case s >= 500:
		return Result{Class: ServerError}
	}
	return Result{Class: ClientError}
}

func classifyErr(err error) Class {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return Timeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return ConnRefused
	}
	// Bağlantı yarıda ölürse istek karşıya varmış olabilir.
	return ConnReset
}

func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if isDigits(v) {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n > int64(MaxRetryAfter/time.Second) {
			return MaxRetryAfter
		}
		return time.Duration(n) * time.Second
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	return min(max(t.Sub(now), 0), MaxRetryAfter)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

const (
	BaseDelay = time.Second
	MaxDelay  = 60 * time.Second
)

func Backoff(attempt int, rng *rand.Rand) time.Duration {
	ceiling := MaxDelay
	if attempt < 6 { // iki üzeri altı tavanı aşar, daha büyük kaydırma taşar
		ceiling = min(BaseDelay<<max(attempt, 0), MaxDelay)
	}
	return time.Duration(rng.Int64N(int64(ceiling) + 1))
}
