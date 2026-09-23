package typesafe

import (
	"errors"
	"math"
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		header map[string]string
		want   time.Duration
		wantOK bool
	}{
		{name: "no headers", header: nil, wantOK: false},
		{name: "seconds", header: map[string]string{"Retry-After": "3"}, want: 3 * time.Second, wantOK: true},
		{name: "fractional seconds", header: map[string]string{"Retry-After": "0.5"}, want: 500 * time.Millisecond, wantOK: true},
		{name: "milliseconds", header: map[string]string{"Retry-After-Ms": "250"}, want: 250 * time.Millisecond, wantOK: true},
		{
			name:   "milliseconds win",
			header: map[string]string{"Retry-After-Ms": "250", "Retry-After": "30"},
			want:   250 * time.Millisecond,
			wantOK: true,
		},
		{
			name:   "http date",
			header: map[string]string{"Retry-After": "Thu, 17 Sep 2026 12:00:20 GMT"},
			want:   20 * time.Second,
			wantOK: true,
		},
		{
			name:   "past http date clamps to zero",
			header: map[string]string{"Retry-After": "Thu, 17 Sep 2026 11:59:00 GMT"},
			want:   0,
			wantOK: true,
		},
		{name: "negative seconds", header: map[string]string{"Retry-After": "-5"}, wantOK: false},
		{name: "seconds beyond a Duration", header: map[string]string{"Retry-After": "1e20"}, wantOK: false},
		{
			name:   "milliseconds beyond a Duration fall back",
			header: map[string]string{"Retry-After-Ms": "1e300", "Retry-After": "3"},
			want:   3 * time.Second,
			wantOK: true,
		},
		{name: "unparseable", header: map[string]string{"Retry-After": "soon"}, wantOK: false},
		{name: "empty", header: map[string]string{"Retry-After": ""}, wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{}
			for name, value := range test.header {
				header.Set(name, value)
			}

			got, ok := parseRetryAfter(header, now)
			if ok != test.wantOK || (ok && got != test.want) {
				t.Errorf("parseRetryAfter() = %v, %v, want %v, %v", got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	policy := DefaultRetryPolicy()
	policy.rand = func() float64 { return 0 }

	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for attempt, expected := range want {
		if got := policy.backoff(attempt); got != expected {
			t.Errorf("backoff(%d) = %v, want %v", attempt, got, expected)
		}
	}
}

func TestBackoffWithoutCapNeverShrinks(t *testing.T) {
	policy := RetryPolicy{BackoffInitial: time.Second, BackoffMax: math.MaxInt64, rand: func() float64 { return 0 }}

	previous := time.Duration(0)
	for attempt := range 100 {
		got := policy.backoff(attempt)
		if got < previous {
			t.Fatalf("backoff(%d) = %v, shorter than backoff(%d) = %v", attempt, got, attempt-1, previous)
		}
		previous = got
	}
}

func TestBackoffSubtractsJitter(t *testing.T) {
	policy := DefaultRetryPolicy()
	policy.rand = func() float64 { return 1 }

	if got, want := policy.backoff(0), 375*time.Millisecond; got != want {
		t.Errorf("backoff(0) with full jitter = %v, want %v", got, want)
	}
}

func TestBackoffDisabled(t *testing.T) {
	policy := DefaultRetryPolicy()
	policy.BackoffInitial = 0

	if got := policy.backoff(3); got != 0 {
		t.Errorf("backoff(3) = %v, want 0 when BackoffInitial is zero", got)
	}
}

func TestDelayPrefersRetryAfter(t *testing.T) {
	policy := DefaultRetryPolicy()
	policy.rand = func() float64 { return 0 }
	header := http.Header{}
	throttled := &APIError{Status: http.StatusTooManyRequests, Header: header}
	header.Set("Retry-After", "2")

	if got := policy.delay(0, throttled); got != 2*time.Second {
		t.Errorf("delay() = %v, want the 2s the server asked for", got)
	}

	header.Set("Retry-After", "600")
	if got := policy.delay(0, throttled); got != 500*time.Millisecond {
		t.Errorf("delay() = %v, want backoff when the server asks for longer than MaxRetryAfter", got)
	}

	header.Set("Retry-After", "1e20")
	if got := policy.delay(0, throttled); got != 500*time.Millisecond {
		t.Errorf("delay() = %v, want backoff when the server asks for longer than a Duration holds", got)
	}

	if got := policy.delay(0, &ConnectionError{}); got != 500*time.Millisecond {
		t.Errorf("delay() = %v, want backoff after a failure with no response", got)
	}

	policy.RespectRetryAfter = false
	header.Set("Retry-After", "2")
	if got := policy.delay(0, throttled); got != 500*time.Millisecond {
		t.Errorf("delay() = %v, want backoff when RespectRetryAfter is off", got)
	}
}

func TestRetries(t *testing.T) {
	policy := DefaultRetryPolicy()
	timeout := error(&TimeoutError{Timeout: time.Second})
	connection := error(&ConnectionError{Message: "connection error"})
	unavailable := error(&APIError{Status: http.StatusServiceUnavailable})
	badRequest := error(&APIError{Status: http.StatusBadRequest})

	for _, err := range []error{timeout, connection, unavailable} {
		if !policy.retries(err) {
			t.Errorf("retries(%v) = false, want true by default", err)
		}
	}
	for _, err := range []error{badRequest, errors.New("something else")} {
		if policy.retries(err) {
			t.Errorf("retries(%v) = true, want false by default", err)
		}
	}

	policy.RetryTimeoutErrors = false
	policy.RetryStatus = func(status int) bool { return status == http.StatusBadRequest }
	if policy.retries(timeout) {
		t.Error("retries(TimeoutError) = true, want false when timeouts are not retried")
	}
	if !policy.retries(connection) {
		t.Error("retries(ConnectionError) = false, want true")
	}
	if policy.retries(unavailable) || !policy.retries(badRequest) {
		t.Error("retries(APIError) ignored RetryStatus")
	}
}

func TestDefaultRetryStatus(t *testing.T) {
	retried := []int{408, 429, 500, 502, 503, 599}
	for _, status := range retried {
		if !DefaultRetryStatus(status) {
			t.Errorf("DefaultRetryStatus(%d) = false, want true", status)
		}
	}
	for _, status := range []int{200, 400, 401, 404, 409, 422} {
		if DefaultRetryStatus(status) {
			t.Errorf("DefaultRetryStatus(%d) = true, want false", status)
		}
	}
}

func TestRetryPolicyValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*RetryPolicy)
		wantErr bool
	}{
		{name: "defaults", mutate: func(*RetryPolicy) {}, wantErr: false},
		{name: "zero policy", mutate: func(p *RetryPolicy) { *p = RetryPolicy{} }, wantErr: false},
		{name: "negative retries", mutate: func(p *RetryPolicy) { p.MaxRetries = -1 }, wantErr: true},
		{name: "negative backoff", mutate: func(p *RetryPolicy) { p.BackoffInitial = -time.Second }, wantErr: true},
		{name: "jitter above one", mutate: func(p *RetryPolicy) { p.BackoffJitter = 1.5 }, wantErr: true},
		{name: "jitter below zero", mutate: func(p *RetryPolicy) { p.BackoffJitter = -0.1 }, wantErr: true},
		{name: "negative max retry after", mutate: func(p *RetryPolicy) { p.MaxRetryAfter = -time.Second }, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultRetryPolicy()
			test.mutate(&policy)

			if err := policy.validate(); (err != nil) != test.wantErr {
				t.Errorf("validate() error = %v, wantErr = %v", err, test.wantErr)
			}
		})
	}
}
