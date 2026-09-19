package typesafe

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name    string
		want    slog.Level
		wantErr bool
	}{
		{name: "debug", want: slog.LevelDebug},
		{name: "INFO", want: slog.LevelInfo},
		{name: " warn ", want: slog.LevelWarn},
		{name: "error", want: slog.LevelError},
		{name: "off", want: LevelOff},
		{name: "chatty", wantErr: true},
		{name: "", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseLogLevel(test.name)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseLogLevel(%q) error = %v, wantErr = %v", test.name, err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Errorf("ParseLogLevel(%q) = %v, want %v", test.name, got, test.want)
			}
		})
	}
}

func TestRedactHeader(t *testing.T) {
	header := http.Header{}
	header.Set("Authorization", "Bearer sk-live-0123456789abcdef")
	header.Set("X-Api-Key", "short")
	header.Set("Cookie", "session=abc123")
	header.Set("X-Tenant", "acme")

	redacted := redactHeader(header)

	want := map[string]string{
		"Authorization": "Bearer ***cdef",
		"X-Api-Key":     "***",
		"Cookie":        "***",
		"X-Tenant":      "acme",
	}
	for name, expected := range want {
		if redacted[name] != expected {
			t.Errorf("redactHeader()[%q] = %q, want %q", name, redacted[name], expected)
		}
	}
}

func TestDebugLoggingRedactsCredentials(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, `{"model":"jev-1","usage":{},"answers":{"a":{"type":"noul","noul":0.4}}}`)
	}, WithLogger(logger))

	if _, err := client.SystemOne(context.Background(), "sensitive state", Questions{"a": Noul{Instructions: "Is this relevant?"}}); err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	output := logged.String()
	if strings.Contains(output, "test-key") {
		t.Error("debug logs leaked the API key")
	}
	if !strings.Contains(output, "Bearer ***") {
		t.Errorf("debug logs did not show a redacted credential, got:\n%s", output)
	}
	if strings.Contains(output, "sensitive state") {
		t.Errorf("debug logs leaked request state, got:\n%s", output)
	}
}

func TestUnsafeDebugBodyLoggingIsOptIn(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, `{"model":"jev-1","usage":{},"answers":{"a":{"type":"noul","noul":0.4}}}`)
	}, WithLogger(logger), WithUnsafeDebugBodyLogging())

	if _, err := client.SystemOne(context.Background(), "sensitive state", Questions{"a": Noul{Instructions: "Is this relevant?"}}); err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	if !strings.Contains(logged.String(), "sensitive state") {
		t.Errorf("unsafe debug logging did not include the request body, got:\n%s", logged.String())
	}
}

func TestDefaultLoggerReadsTheEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv(LogLevelEnv, "debug")

	logger, err := defaultLogger()
	if err != nil {
		t.Fatalf("defaultLogger() error = %v", err)
	}
	if !logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("defaultLogger() ignored TYPESAFE_LOG_LEVEL=debug")
	}

	t.Setenv(LogLevelEnv, "off")
	logger, err = defaultLogger()
	if err != nil {
		t.Fatalf("defaultLogger() error = %v", err)
	}
	if logger.Enabled(context.Background(), slog.LevelError) {
		t.Error("defaultLogger() still logged with TYPESAFE_LOG_LEVEL=off")
	}
}
