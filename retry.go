package typesafe

import (
	"errors"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryPolicy controls how failed attempts are retried. Start from
// [DefaultRetryPolicy] and change the fields you care about; a zero RetryPolicy
// disables retries entirely.
//
//	policy := typesafe.DefaultRetryPolicy()
//	policy.MaxRetries = 5
//	client, err := typesafe.New(typesafe.WithRetry(policy))
//
// There is no total retry budget: bound the whole call, attempts and backoff
// together, with a context deadline.
type RetryPolicy struct {
	// MaxRetries is the number of retries after the initial attempt; 0 disables
	// retries.
	MaxRetries int
	// BackoffInitial is the first backoff delay, doubled each attempt up to
	// BackoffMax; zero disables backoff.
	BackoffInitial time.Duration
	// BackoffMax caps the backoff delay.
	BackoffMax time.Duration
	// BackoffJitter is the fraction of each backoff delay randomly subtracted,
	// from 0 to 1.
	BackoffJitter float64
	// RetryStatus reports whether a response status should be retried. When nil,
	// [DefaultRetryStatus] applies.
	RetryStatus func(status int) bool
	// RespectRetryAfter honors the retry-after-ms and Retry-After response
	// headers in place of backoff.
	RespectRetryAfter bool
	// MaxRetryAfter caps the server-requested delay; a longer one falls back to
	// backoff.
	MaxRetryAfter time.Duration
	// RetryConnectionErrors retries a [ConnectionError], including a response
	// body that stopped arriving midway.
	RetryConnectionErrors bool
	// RetryTimeoutErrors retries a [TimeoutError].
	RetryTimeoutErrors bool

	// rand supplies the jitter fraction; tests replace it to remove randomness.
	rand func() float64
}

// DefaultRetryPolicy returns the SDK defaults: two retries with exponential
// backoff from 500ms to 5s, honoring Retry-After up to a minute, for HTTP 408,
// 429 and 5xx responses as well as connection errors and timeouts.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:            2,
		BackoffInitial:        500 * time.Millisecond,
		BackoffMax:            5 * time.Second,
		BackoffJitter:         0.25,
		RetryStatus:           DefaultRetryStatus,
		RespectRetryAfter:     true,
		MaxRetryAfter:         60 * time.Second,
		RetryConnectionErrors: true,
		RetryTimeoutErrors:    true,
	}
}

// DefaultRetryStatus reports whether a status is retried by default: 408, 429,
// and any 5xx.
func DefaultRetryStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

func (p RetryPolicy) validate() error {
	if p.MaxRetries < 0 {
		return newError("retry MaxRetries must not be negative, got %d", p.MaxRetries)
	}
	if p.BackoffInitial < 0 {
		return newError("retry BackoffInitial must not be negative, got %s", p.BackoffInitial)
	}
	if p.BackoffMax < 0 {
		return newError("retry BackoffMax must not be negative, got %s", p.BackoffMax)
	}
	if p.MaxRetryAfter < 0 {
		return newError("retry MaxRetryAfter must not be negative, got %s", p.MaxRetryAfter)
	}
	if p.BackoffJitter < 0 || p.BackoffJitter > 1 || math.IsNaN(p.BackoffJitter) {
		return newError("retry BackoffJitter must be between 0 and 1, got %v", p.BackoffJitter)
	}
	return nil
}

func (p RetryPolicy) retriesStatus(status int) bool {
	if p.RetryStatus == nil {
		return DefaultRetryStatus(status)
	}
	return p.RetryStatus(status)
}

func (p RetryPolicy) retriesError(err error) bool {
	if _, ok := errors.AsType[*TimeoutError](err); ok {
		return p.RetryTimeoutErrors
	}
	if _, ok := errors.AsType[*ConnectionError](err); ok {
		return p.RetryConnectionErrors
	}
	return false
}

// delay returns how long to wait before the retry following a zero-based
// attempt, preferring a server-requested delay within MaxRetryAfter.
func (p RetryPolicy) delay(attempt int, header http.Header, now time.Time) time.Duration {
	if p.RespectRetryAfter && header != nil {
		if requested, ok := parseRetryAfter(header, now); ok && requested <= p.MaxRetryAfter {
			return requested
		}
	}
	return p.backoff(attempt)
}

func (p RetryPolicy) backoff(attempt int) time.Duration {
	if p.BackoffInitial <= 0 || p.BackoffMax <= 0 {
		return 0
	}
	// Neither step can overflow: the shift is checked against BackoffMax before
	// it is taken, and the jitter is subtracted instead of scaling a float that
	// rounds up past math.MaxInt64 when BackoffMax is near it.
	exponential := p.BackoffMax
	if p.BackoffInitial <= p.BackoffMax>>attempt {
		exponential = p.BackoffInitial << attempt
	}
	random := rand.Float64
	if p.rand != nil {
		random = p.rand
	}
	jitter := time.Duration(float64(exponential) * random() * p.BackoffJitter)
	return exponential - min(jitter, exponential)
}

// parseRetryAfter reads retry-after-ms, then Retry-After as either seconds or an
// HTTP date.
func parseRetryAfter(header http.Header, now time.Time) (time.Duration, bool) {
	if raw := strings.TrimSpace(header.Get("Retry-After-Ms")); raw != "" {
		if ms, err := strconv.ParseFloat(raw, 64); err == nil {
			if delay, ok := durationOf(ms, time.Millisecond); ok {
				return delay, true
			}
		}
	}
	raw := strings.TrimSpace(header.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		return durationOf(seconds, time.Second)
	}
	if at, err := http.ParseTime(raw); err == nil {
		return max(0, at.Sub(now)), true
	}
	return 0, false
}

// durationOf rejects a count of units that is negative, NaN, or too long for a
// Duration: Go leaves an out-of-range float conversion to the platform, and on
// amd64 it comes out negative.
func durationOf(count float64, unit time.Duration) (time.Duration, bool) {
	ns := count * float64(unit)
	if !(ns >= 0 && ns < math.MaxInt64) {
		return 0, false
	}
	return time.Duration(ns), true
}
