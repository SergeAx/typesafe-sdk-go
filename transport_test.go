package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastRetry keeps the retry tests honest about ordering without making them wait.
func fastRetry() RetryPolicy {
	policy := DefaultRetryPolicy()
	policy.BackoffInitial = time.Millisecond
	policy.BackoffMax = time.Millisecond
	policy.BackoffJitter = 0
	return policy
}

func testClient(t *testing.T, handler http.HandlerFunc, options ...ClientOption) *Client {
	t.Helper()
	clearEnv(t)

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	options = append([]ClientOption{
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithRetry(fastRetry()),
	}, options...)
	client, err := New(options...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.Header().Set("Content-Type", jsonContentType)
	w.Header().Set(requestIDHeader, "req_test")
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("writing the test response: %v", err)
	}
}

func TestSystemOneSendsAWellFormedRequest(t *testing.T) {
	var (
		method string
		path   string
		header http.Header
		body   map[string]any
	)
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, header = r.Method, r.URL.Path, r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding the request body: %v", err)
		}
		writeJSON(t, w, http.StatusOK, `{"model":"jev-1","usage":{},"answers":{"billing":{"type":"noul","noul":0.9}}}`)
	})

	result, err := client.SystemOne(t.Context(), "I was charged twice.", Questions{
		"billing": Noul{Instructions: "Is this about billing?"},
	})
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	if method != http.MethodPost || path != systemOnePath {
		t.Errorf("request = %s %s, want POST %s", method, path, systemOnePath)
	}
	if got := header.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
	}
	if got := header.Get(sdkHeader); got != sdkName+"/"+Version {
		t.Errorf("%s = %q, want %q", sdkHeader, got, sdkName+"/"+Version)
	}
	if got := header.Get(runtimeHeader); !strings.HasPrefix(got, "go/") {
		t.Errorf("%s = %q, want a go/... runtime", runtimeHeader, got)
	}
	if got := header.Get(retryCountHeader); got != "" {
		t.Errorf("%s = %q, want it absent on the first attempt", retryCountHeader, got)
	}

	if body["state"] != "I was charged twice." {
		t.Errorf("state = %v, want the text passed in", body["state"])
	}
	if body["model"] != DefaultModel {
		t.Errorf("model = %v, want the client default %q", body["model"], DefaultModel)
	}
	questions, ok := body["questions"].(map[string]any)
	if !ok || questions["billing"] == nil {
		t.Fatalf("questions = %v, want the billing question", body["questions"])
	}

	if result.RequestID != "req_test" {
		t.Errorf("RequestID = %q, want %q", result.RequestID, "req_test")
	}
	if got := result.Nouls()["billing"].Noul; got != 0.9 {
		t.Errorf("Nouls()[billing].Noul = %v, want 0.9", got)
	}
}

func TestSystemOneModelAndExtraBodyOverrides(t *testing.T) {
	var body map[string]any
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding the request body: %v", err)
		}
		writeJSON(t, w, http.StatusOK, `{"model":"jev-1","usage":{},"answers":{"a":{"type":"noul","noul":0.1}}}`)
	})

	_, err := client.SystemOne(t.Context(), "state", Questions{"a": Noul{Instructions: "Is it?"}},
		WithModel("jev-mini"),
		WithExtraBody(map[string]any{"trace_id": "t-1", "state": "replaced"}),
	)
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	if body["model"] != "jev-mini" {
		t.Errorf("model = %v, want the per-call override", body["model"])
	}
	if body["trace_id"] != "t-1" {
		t.Errorf("trace_id = %v, want the extra body field", body["trace_id"])
	}
	if body["state"] != "replaced" {
		t.Errorf("state = %v, want the extra body to win the collision", body["state"])
	}
}

func TestSystemOneValidatesBeforeSending(t *testing.T) {
	var called atomic.Bool
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		writeJSON(t, w, http.StatusOK, `{}`)
	})

	if _, err := client.SystemOne(t.Context(), "state", Questions{}); err == nil {
		t.Error("SystemOne() accepted an empty question set, want an error")
	}
	if called.Load() {
		t.Error("SystemOne() sent a request for an invalid question set")
	}
}

func TestSystemOneRetriesServerErrors(t *testing.T) {
	var attempts atomic.Int32
	var retryCounts []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		retryCounts = append(retryCounts, r.Header.Get(retryCountHeader))
		if attempts.Add(1) < 3 {
			writeJSON(t, w, http.StatusInternalServerError, `{"error":"try again"}`)
			return
		}
		writeJSON(t, w, http.StatusOK, `{"model":"jev-1","usage":{},"answers":{"a":{"type":"noul","noul":0.4}}}`)
	})

	result, err := client.SystemOne(t.Context(), "state", Questions{"a": Noul{Instructions: "Is it?"}})
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
	if want := []string{"", "1", "2"}; !equalStrings(retryCounts, want) {
		t.Errorf("%s across attempts = %v, want %v", retryCountHeader, retryCounts, want)
	}
	if got := result.Nouls()["a"].Noul; got != 0.4 {
		t.Errorf("Nouls()[a].Noul = %v, want 0.4", got)
	}
}

func TestSystemOneReturnsTheLastErrorWhenRetriesRunOut(t *testing.T) {
	var attempts atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		writeJSON(t, w, http.StatusServiceUnavailable, `{"error":"still down"}`)
	})

	_, err := client.SystemOne(t.Context(), "state", Questions{"a": Noul{Instructions: "Is it?"}})

	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatalf("SystemOne() error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != http.StatusServiceUnavailable || apiErr.Message != "still down" {
		t.Errorf("error = %+v, want 503 still down", apiErr)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want the initial attempt plus two retries", got)
	}
	if !errors.Is(err, ErrInternalServer) {
		t.Error("errors.Is(err, ErrInternalServer) = false, want true")
	}
}

func TestSystemOneDoesNotRetryClientErrors(t *testing.T) {
	var attempts atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		writeJSON(t, w, http.StatusUnauthorized, `{"error":"Invalid API key"}`)
	})

	_, err := client.SystemOne(t.Context(), "state", Questions{"a": Noul{Instructions: "Is it?"}})

	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("SystemOne() error = %v, want an authentication failure", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

func TestSystemOneHonorsRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After-Ms", "20")
			writeJSON(t, w, http.StatusTooManyRequests, `{"error":"slow down"}`)
			return
		}
		writeJSON(t, w, http.StatusOK, `{"model":"jev-1","usage":{},"answers":{"a":{"type":"noul","noul":0.4}}}`)
	})

	started := time.Now()
	if _, err := client.SystemOne(t.Context(), "state", Questions{"a": Noul{Instructions: "Is it?"}}); err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Errorf("retried after %v, want at least the 20ms the server asked for", elapsed)
	}
}

func TestSystemOneRetriesConnectionErrors(t *testing.T) {
	var attempts atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("the test server does not support hijacking")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijacking the connection: %v", err)
				return
			}
			conn.Close()
			return
		}
		writeJSON(t, w, http.StatusOK, `{"model":"jev-1","usage":{},"answers":{"a":{"type":"noul","noul":0.4}}}`)
	})

	if _, err := client.SystemOne(t.Context(), "state", Questions{"a": Noul{Instructions: "Is it?"}}); err != nil {
		t.Fatalf("SystemOne() error = %v, want the dropped connection to be retried", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

func TestSystemOneTimesOutPerAttempt(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		stall(r)
	}, WithTimeout(30*time.Millisecond), WithRetry(RetryPolicy{}))

	_, err := client.SystemOne(t.Context(), "state", Questions{"a": Noul{Instructions: "Is it?"}})

	timeout, ok := errors.AsType[*TimeoutError](err)
	if !ok {
		t.Fatalf("SystemOne() error = %v (%T), want *TimeoutError", err, err)
	}
	if timeout.Timeout != 30*time.Millisecond {
		t.Errorf("Timeout = %v, want 30ms", timeout.Timeout)
	}
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, ErrConnection) {
		t.Error("a timeout should match both ErrTimeout and ErrConnection")
	}
}

func TestSystemOneStopsWhenTheContextIsCanceled(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		stall(r)
	})

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := client.SystemOne(ctx, "state", Questions{"a": Noul{Instructions: "Is it?"}})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SystemOne() error = %v, want it to match context.Canceled", err)
	}
	if _, ok := errors.AsType[*TimeoutError](err); ok {
		t.Error("a caller cancellation was reported as a timeout")
	}
}

func TestSystemOneReportsNonJSONErrorBodies(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		if _, err := io.WriteString(w, "<html>502 Bad Gateway</html>"); err != nil {
			t.Errorf("writing the test response: %v", err)
		}
	}, WithRetry(RetryPolicy{}))

	_, err := client.SystemOne(t.Context(), "state", Questions{"a": Noul{Instructions: "Is it?"}})

	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatalf("SystemOne() error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Body != "<html>502 Bad Gateway</html>" {
		t.Errorf("Body = %v, want the raw response text", apiErr.Body)
	}
}

func TestModelsList(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != modelsPath {
			t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, modelsPath)
		}
		writeJSON(t, w, http.StatusOK,
			`{"models":[{"name":"jev-latest","description":"General-purpose system one model.","release_date":"2026-09-15"}]}`)
	})

	models, err := client.Models.List(t.Context())
	if err != nil {
		t.Fatalf("Models.List() error = %v", err)
	}
	want := ModelMetadata{Name: "jev-latest", Description: "General-purpose system one model.", ReleaseDate: "2026-09-15"}
	if len(models) != 1 || models[0] != want {
		t.Errorf("Models.List() = %+v, want [%+v]", models, want)
	}
}

func TestModelsListRejectsAnUnexpectedShape(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, `{"data":[]}`)
	})

	_, err := client.Models.List(t.Context())

	invalid, ok := errors.AsType[*ResponseValidationError](err)
	if !ok {
		t.Fatalf("Models.List() error = %v (%T), want *ResponseValidationError", err, err)
	}
	if invalid.Field != "models" {
		t.Errorf("Field = %q, want %q", invalid.Field, "models")
	}
}

func TestModelsListNamesAMistypedField(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, `{"models":"none"}`)
	})

	_, err := client.Models.List(t.Context())

	invalid, ok := errors.AsType[*ResponseValidationError](err)
	if !ok {
		t.Fatalf("Models.List() error = %v (%T), want *ResponseValidationError", err, err)
	}
	if invalid.Field != "models" {
		t.Errorf("Field = %q, want %q; the body is valid JSON", invalid.Field, "models")
	}
}

// stall holds a response open until the client gives up. The bound matters:
// a handler that only waits on the request context can outlive the client on
// platforms where the server notices a dropped connection late, and then the
// test server's shutdown never completes.
func stall(r *http.Request) {
	select {
	case <-r.Context().Done():
	case <-time.After(500 * time.Millisecond):
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func FuzzSendRetries(f *testing.F) {
	f.Add(uint8(2), []byte{3, 3, 0})
	f.Add(uint8(2), []byte{3, 3, 3, 0})
	f.Add(uint8(0), []byte{2})
	f.Add(uint8(5), []byte{4, 5, 2, 1, 0})
	f.Fuzz(func(t *testing.T, maxRetries uint8, script []byte) {
		// Each byte scripts one attempt: 200, 400, or one of the retryable 429,
		// 503, dropped connection, and timeout. Attempts past the script succeed.
		outcome := func(attempt int) byte {
			if attempt < len(script) {
				return script[attempt] % 6
			}
			return 0
		}
		policy := RetryPolicy{MaxRetries: int(maxRetries % 8), RetryConnectionErrors: true, RetryTimeoutErrors: true}

		attempts := 0
		transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			want := ""
			if attempts > 0 {
				want = strconv.Itoa(attempts)
			}
			if got := r.Header.Get(retryCountHeader); got != want {
				t.Errorf("attempt %d sent %s %q, want %q", attempts, retryCountHeader, got, want)
			}
			attempts++
			switch outcome(attempts - 1) {
			case 4:
				return nil, errors.New("connection reset")
			case 5:
				return nil, context.DeadlineExceeded
			}
			status := [...]int{http.StatusOK, http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable}[outcome(attempts-1)]
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"models":[]}`))}, nil
		})
		client, err := New(WithAPIKey("test-key"), WithBaseURL("http://api.test"), WithRetry(policy),
			WithHTTPClient(&http.Client{Transport: transport}), WithLogger(slog.New(slog.DiscardHandler)))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		_, err = client.Models.List(t.Context())

		want := 1
		for outcome(want-1) >= 2 && want <= policy.MaxRetries {
			want++
		}
		if attempts != want {
			t.Errorf("attempts = %d, want %d for script %v and MaxRetries %d", attempts, want, script, policy.MaxRetries)
		}
		if succeeded := outcome(attempts-1) == 0; (err == nil) != succeeded {
			t.Errorf("Models.List() error = %v after attempt outcome %d", err, outcome(attempts-1))
		}
	})
}
