package typesafe

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAPIErrorCategories(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{status: http.StatusBadRequest, want: ErrBadRequest},
		{status: http.StatusUnauthorized, want: ErrAuthentication},
		{status: http.StatusForbidden, want: ErrPermissionDenied},
		{status: http.StatusNotFound, want: ErrNotFound},
		{status: http.StatusUnprocessableEntity, want: ErrUnprocessableEntity},
		{status: http.StatusTooManyRequests, want: ErrRateLimit},
		{status: http.StatusInternalServerError, want: ErrInternalServer},
		{status: http.StatusBadGateway, want: ErrInternalServer},
	}

	for _, test := range tests {
		err := error(newAPIError(test.status, http.Header{}, nil, ""))
		if !errors.Is(err, test.want) {
			t.Errorf("errors.Is(%d, %v) = false, want true", test.status, test.want)
		}
		if errors.Is(err, ErrConnection) {
			t.Errorf("errors.Is(%d, ErrConnection) = true, want false", test.status)
		}
	}
}

func TestAPIErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		body any
		want string
	}{
		{name: "plain text body", body: "Upstream is down", want: "Upstream is down"},
		{name: "error string", body: map[string]any{"error": "Invalid API key"}, want: "Invalid API key"},
		{
			name: "nested error message",
			body: map[string]any{"error": map[string]any{"message": "Invalid API key"}},
			want: "Invalid API key",
		},
		{name: "message field", body: map[string]any{"message": "Try later"}, want: "Try later"},
		{name: "detail string", body: map[string]any{"detail": "Not found"}, want: "Not found"},
		{
			name: "validation errors",
			body: map[string]any{"detail": []any{
				map[string]any{"loc": []any{"body", "questions", "urgency", "criteria"}, "msg": "Field required"},
				map[string]any{"loc": []any{"body", "state"}, "msg": "Input should be a string"},
			}},
			want: "questions.urgency.criteria: Field required; state: Input should be a string",
		},
		{name: "no body", body: nil, want: "status code (no body)"},
		{name: "unrecognized shape", body: map[string]any{"oops": true}, want: `{"oops":true}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := newAPIError(http.StatusBadRequest, http.Header{}, test.body, "")
			if err.Message != test.want {
				t.Errorf("Message = %q, want %q", err.Message, test.want)
			}
		})
	}
}

func TestAPIErrorTruncatesLongBodies(t *testing.T) {
	err := newAPIError(http.StatusBadGateway, http.Header{}, strings.Repeat("x", 500), "")

	if !strings.HasSuffix(err.Message, "…") {
		t.Errorf("Message = %q, want it truncated with an ellipsis", err.Message)
	}
	if got := len([]rune(err.Message)); got != maxErrorBodyLength+1 {
		t.Errorf("len(Message) = %d runes, want %d", got, maxErrorBodyLength+1)
	}
}

func TestAPIErrorString(t *testing.T) {
	header := http.Header{}
	header.Set(requestIDHeader, "req_42")
	err := newAPIError(http.StatusUnauthorized, header, map[string]any{"error": "Invalid API key"},
		"POST https://api.typesafe.ai/v1/systemone")

	want := "typesafe: POST https://api.typesafe.ai/v1/systemone: 401 Invalid API key (request_id=req_42)"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if err.RequestID != "req_42" {
		t.Errorf("RequestID = %q, want %q", err.RequestID, "req_42")
	}
}

func TestAPIErrorRetryAfter(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "3")
	err := newAPIError(http.StatusTooManyRequests, header, nil, "")

	delay, ok := err.RetryAfter()
	if !ok || delay != 3*time.Second {
		t.Errorf("RetryAfter() = %v, %v, want 3s, true", delay, ok)
	}

	if _, ok := newAPIError(http.StatusTooManyRequests, http.Header{}, nil, "").RetryAfter(); ok {
		t.Error("RetryAfter() reported a delay for a response without the header")
	}
}

func TestTimeoutErrorMatchesConnection(t *testing.T) {
	err := error(&TimeoutError{Timeout: 2 * time.Second, Err: context.DeadlineExceeded})

	for _, target := range []error{ErrTimeout, ErrConnection, context.DeadlineExceeded} {
		if !errors.Is(err, target) {
			t.Errorf("errors.Is(TimeoutError, %v) = false, want true", target)
		}
	}
	if timeout, ok := errors.AsType[*TimeoutError](err); !ok || timeout.Timeout != 2*time.Second {
		t.Errorf("errors.AsType() did not recover the timeout, got %v", timeout)
	}
}

func TestConnectionErrorUnwraps(t *testing.T) {
	cause := errors.New("dial tcp: no route to host")
	err := error(&ConnectionError{Message: "connection error: " + cause.Error(), Err: cause})

	if !errors.Is(err, ErrConnection) {
		t.Error("errors.Is(ConnectionError, ErrConnection) = false, want true")
	}
	if errors.Is(err, ErrTimeout) {
		t.Error("errors.Is(ConnectionError, ErrTimeout) = true, want false")
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is() did not reach the wrapped cause")
	}
}

func TestErrorUnwraps(t *testing.T) {
	cause := errors.New("unsupported type")
	err := error(&Error{Message: "the request body could not be encoded as JSON", Err: cause})

	if !errors.Is(err, cause) {
		t.Error("errors.Is() did not reach the wrapped cause")
	}
	want := "typesafe: the request body could not be encoded as JSON: unsupported type"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func FuzzTruncate(f *testing.F) {
	f.Add("short")
	f.Add(strings.Repeat("é", maxErrorBodyLength+50))
	f.Add(strings.Repeat("x", maxErrorBodyLength-1) + "日本")
	f.Add(strings.Repeat("\xff", maxErrorBodyLength+1))
	f.Fuzz(func(t *testing.T, text string) {
		got := truncate(text)

		if n := utf8.RuneCountInString(got); n > maxErrorBodyLength+1 {
			t.Fatalf("truncate() kept %d runes, want at most %d", n, maxErrorBodyLength+1)
		}
		if !utf8.ValidString(text) {
			return
		}
		kept, cut := strings.CutSuffix(got, "…")
		switch {
		case utf8.RuneCountInString(text) <= maxErrorBodyLength:
			if got != text {
				t.Fatalf("truncate() changed a text short enough to keep: %q", got)
			}
		case !cut || !strings.HasPrefix(text, kept) || utf8.RuneCountInString(kept) != maxErrorBodyLength:
			t.Fatalf("truncate() = %q, want the first %d runes and an ellipsis", got, maxErrorBodyLength)
		}
	})
}
