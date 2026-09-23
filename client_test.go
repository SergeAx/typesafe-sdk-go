package typesafe

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewRequiresAPIKey(t *testing.T) {
	clearEnv(t)

	_, err := New()

	sdkErr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("New() error = %v (%T), want *typesafe.Error", err, err)
	}
	if got := sdkErr.Error(); got == "" || !strings.Contains(got, APIKeyEnv) {
		t.Errorf("New() error = %q, want it to name %s", got, APIKeyEnv)
	}
}

func TestNewResolvesConfiguration(t *testing.T) {
	clearEnv(t)
	t.Setenv(APIKeyEnv, "  key-from-env  ")
	t.Setenv(BaseURLEnv, "https://env.example.com/")
	t.Setenv(DefaultModelEnv, "model-from-env")

	client, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.apiKey != "key-from-env" {
		t.Errorf("apiKey = %q, want the trimmed environment value", client.apiKey)
	}
	if client.BaseURL() != "https://env.example.com" {
		t.Errorf("BaseURL() = %q, want trailing slashes stripped", client.BaseURL())
	}
	if client.DefaultModel() != "model-from-env" {
		t.Errorf("DefaultModel() = %q, want %q", client.DefaultModel(), "model-from-env")
	}
}

func TestNewOptionsBeatEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv(APIKeyEnv, "key-from-env")
	t.Setenv(BaseURLEnv, "https://env.example.com")
	t.Setenv(DefaultModelEnv, "model-from-env")

	client, err := New(
		WithAPIKey("key-from-code"),
		WithBaseURL("https://code.example.com"),
		WithDefaultModel("model-from-code"),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.apiKey != "key-from-code" {
		t.Errorf("apiKey = %q, want the option to win", client.apiKey)
	}
	if client.BaseURL() != "https://code.example.com" {
		t.Errorf("BaseURL() = %q, want the option to win", client.BaseURL())
	}
	if client.DefaultModel() != "model-from-code" {
		t.Errorf("DefaultModel() = %q, want the option to win", client.DefaultModel())
	}
}

func TestNewFallsBackToSDKDefaults(t *testing.T) {
	clearEnv(t)

	client, err := New(WithAPIKey("key"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.BaseURL() != DefaultBaseURL {
		t.Errorf("BaseURL() = %q, want %q", client.BaseURL(), DefaultBaseURL)
	}
	if client.DefaultModel() != DefaultModel {
		t.Errorf("DefaultModel() = %q, want %q", client.DefaultModel(), DefaultModel)
	}
	if client.timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", client.timeout, DefaultTimeout)
	}
	if client.retry.MaxRetries != DefaultRetryPolicy().MaxRetries {
		t.Errorf("retry.MaxRetries = %d, want the default", client.retry.MaxRetries)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		options []ClientOption
	}{
		{name: "negative timeout", options: []ClientOption{WithTimeout(-time.Second)}},
		{
			name:    "invalid retry policy",
			options: []ClientOption{WithRetry(RetryPolicy{MaxRetries: -1})},
		},
		{name: "unknown log level", env: map[string]string{LogLevelEnv: "chatty"}},
		{name: "empty base URL", options: []ClientOption{WithBaseURL("/")}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearEnv(t)
			for name, value := range test.env {
				t.Setenv(name, value)
			}

			_, err := New(append([]ClientOption{WithAPIKey("key")}, test.options...)...)

			if _, ok := errors.AsType[*Error](err); !ok {
				t.Fatalf("New() error = %v (%T), want *typesafe.Error", err, err)
			}
		})
	}
}

func TestWithRetryDisablesRetries(t *testing.T) {
	clearEnv(t)

	client, err := New(WithAPIKey("key"), WithRetry(RetryPolicy{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.retry.MaxRetries != 0 {
		t.Errorf("retry.MaxRetries = %d, want 0 for an explicit zero policy", client.retry.MaxRetries)
	}
}

func TestRequestOptionsOverrideClientSettings(t *testing.T) {
	clearEnv(t)
	client, err := New(WithAPIKey("key"), WithTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resolved, err := client.resolve([]RequestOption{
		WithRequestTimeout(time.Second),
		WithRequestRetry(RetryPolicy{MaxRetries: 7}),
		WithRequestHeader("X-Trace", "abc"),
		WithModel("other-model"),
	})
	if err != nil {
		t.Fatalf("resolve() error = %v", err)
	}
	if resolved.timeout != time.Second {
		t.Errorf("timeout = %v, want 1s", resolved.timeout)
	}
	if resolved.retry.MaxRetries != 7 {
		t.Errorf("retry.MaxRetries = %d, want 7", resolved.retry.MaxRetries)
	}
	if resolved.header.Get("X-Trace") != "abc" {
		t.Errorf("header X-Trace = %q, want %q", resolved.header.Get("X-Trace"), "abc")
	}
	if resolved.model != "other-model" {
		t.Errorf("model = %q, want %q", resolved.model, "other-model")
	}

	if _, err := client.resolve([]RequestOption{WithRequestTimeout(0)}); err == nil {
		t.Error("resolve() accepted a zero request timeout, want an error")
	}
}

func TestRequestHeaderProtectsSDKHeaders(t *testing.T) {
	clearEnv(t)
	client, err := New(WithAPIKey("secret-key"), WithHeader("X-Tenant", "acme"), WithHeader("Authorization", "Bearer nope"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	header := client.requestHeader(http.Header{retryCountHeader: []string{"9"}, "Accept": []string{"text/plain"}}, true)

	if got := header.Get("Authorization"); got != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want the SDK's own credential", got)
	}
	if got := header.Get("Accept"); got != jsonContentType {
		t.Errorf("Accept = %q, want %q", got, jsonContentType)
	}
	if got := header.Get(retryCountHeader); got != "" {
		t.Errorf("%s = %q, want it dropped from caller headers", retryCountHeader, got)
	}
	if got := header.Get("X-Tenant"); got != "acme" {
		t.Errorf("X-Tenant = %q, want the client header to survive", got)
	}
	if got := header.Get("Content-Type"); got != jsonContentType {
		t.Errorf("Content-Type = %q, want %q", got, jsonContentType)
	}
	if got := header.Get("User-Agent"); got != sdkName+"/"+Version {
		t.Errorf("User-Agent = %q, want %q", got, sdkName+"/"+Version)
	}
}
