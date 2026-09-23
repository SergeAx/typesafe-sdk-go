package typesafe

import (
	"bytes"
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

	if _, err := client.SystemOne(t.Context(), "state", Questions{"a": Noul{Instructions: "Is it?"}}); err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	output := logged.String()
	if strings.Contains(output, "test-key") {
		t.Error("debug logs leaked the API key")
	}
	if !strings.Contains(output, "Bearer ***") {
		t.Errorf("debug logs did not show a redacted credential, got:\n%s", output)
	}
}

func TestDefaultLoggerReadsTheEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv(LogLevelEnv, "debug")

	logger, err := defaultLogger()
	if err != nil {
		t.Fatalf("defaultLogger() error = %v", err)
	}
	if !logger.Enabled(t.Context(), slog.LevelDebug) {
		t.Error("defaultLogger() ignored TYPESAFE_LOG_LEVEL=debug")
	}

	t.Setenv(LogLevelEnv, "off")
	logger, err = defaultLogger()
	if err != nil {
		t.Fatalf("defaultLogger() error = %v", err)
	}
	if logger.Enabled(t.Context(), slog.LevelError) {
		t.Error("defaultLogger() still logged with TYPESAFE_LOG_LEVEL=off")
	}
}

func FuzzRedactAuthorization(f *testing.F) {
	f.Add("test-key")
	f.Add("sk-live-0123456789abcdef")
	f.Add("  a key with spaces  ")
	f.Fuzz(func(t *testing.T, key string) {
		got := redactHeader(http.Header{"Authorization": {"Bearer " + key}})["Authorization"]

		secret := strings.TrimSpace(key)
		tail, ok := strings.CutPrefix(got, "Bearer ***")
		if !ok || len(tail) > 4 || !strings.HasSuffix(secret, tail) || (len(secret) <= 8 && tail != "") {
			t.Fatalf("redacted %q to %q, want \"Bearer ***\" and at most the last 4 bytes of a secret longer than 8", key, got)
		}
	})
}
