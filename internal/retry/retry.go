// Package retry classifies the outcome of an HTTP call and computes backoff
// delays (rule 6). The LLM client and the provider client share it, so both
// retry the same failures the same way.
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

// Class is the outcome of one call. The names are the error_class labels in
// the database and in metrics.
type Class string

const (
	Success     Class = "success"
	Timeout     Class = "timeout"      // 408, client timeout, context deadline
	RateLimited Class = "rate_limited" // 429
	ServerError Class = "server_error" // 5xx, 529 included
	ConnRefused Class = "conn_refused" // nothing listening: the request never arrived
	ConnReset   Class = "conn_reset"   // the connection died: the request may have arrived
	ClientError Class = "client_error" // any other status: never retried
)

// Retryable reports whether a call with this outcome may be tried again.
// Timeouts and resets are ambiguous; retrying them is safe only because every
// retry carries the same idempotency key.
func (c Class) Retryable() bool {
	switch c {
	case Timeout, RateLimited, ServerError, ConnRefused, ConnReset:
		return true
	}
	return false
}

// MaxRetryAfter caps a server's Retry-After, so a hostile or broken value
// cannot park a job for hours.
const MaxRetryAfter = 60 * time.Second

// Result is a classified outcome. RetryAfter is set only for a 429 that
// carries a usable Retry-After header.
type Result struct {
	Class      Class
	RetryAfter time.Duration
}

// Classify classifies a response or a transport error. err wins over resp,
// so a body read error can be passed with a nil resp. now resolves a
// Retry-After given as an HTTP date.
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
	// ECONNRESET, EPIPE, EOF and unexpected EOF all mean the connection died
	// mid-call. Any other transport error is treated the same way: the
	// request may or may not have reached the server.
	return ConnReset
}

// retryAfter parses delay-seconds or an HTTP date, clamped to [0, cap].
// Anything unparsable is 0, so the caller falls back to its own backoff.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if isDigits(v) {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n > int64(MaxRetryAfter/time.Second) {
			return MaxRetryAfter // only a range error is possible here
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

// Backoff base and cap.
const (
	BaseDelay = time.Second
	MaxDelay  = 60 * time.Second
)

// Backoff returns the delay before retry number attempt (0 is the first
// retry): full jitter, uniform over [0, min(MaxDelay, BaseDelay * 2^attempt)].
// The random source is injected so tests can fix it.
func Backoff(attempt int, rng *rand.Rand) time.Duration {
	ceiling := MaxDelay
	if attempt < 6 { // 2^6 s already exceeds MaxDelay; larger shifts overflow
		ceiling = min(BaseDelay<<max(attempt, 0), MaxDelay)
	}
	return time.Duration(rng.Int64N(int64(ceiling) + 1))
}
