package typesafe

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Client talks to the TypeSafe AI API. It is safe for concurrent use.
type Client struct {
	apiKey       string
	baseURL      string
	defaultModel string
	timeout      time.Duration
	retry        RetryPolicy
	retrySet     bool
	header       http.Header
	httpClient   *http.Client
	logger       *slog.Logger
	logBodies    bool
	requests     atomic.Uint64

	// Models lists the models available to the account.
	Models *Models
}

// ClientOption configures a [Client]. Options take precedence over environment
// variables, which take precedence over the SDK defaults.
type ClientOption func(*Client)

// WithAPIKey sets the API key, overriding TYPESAFE_API_KEY.
func WithAPIKey(key string) ClientOption {
	return func(c *Client) { c.apiKey = key }
}

// WithBaseURL sets the API root, overriding TYPESAFE_BASE_URL.
func WithBaseURL(url string) ClientOption {
	return func(c *Client) { c.baseURL = url }
}

// WithDefaultModel sets the model used by requests that do not name one,
// overriding TYPESAFE_DEFAULT_MODEL.
func WithDefaultModel(model string) ClientOption {
	return func(c *Client) { c.defaultModel = model }
}

// WithTimeout sets the timeout for a single attempt. Retries each get the full
// timeout; bound the call as a whole with a context deadline.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) { c.timeout = timeout }
}

// WithRetry replaces the retry policy. Start from [DefaultRetryPolicy], or pass
// a zero [RetryPolicy] to disable retries.
func WithRetry(policy RetryPolicy) ClientOption {
	return func(c *Client) { c.retry, c.retrySet = policy, true }
}

// WithHeader adds a header to every request. SDK headers, including
// authentication, cannot be overridden.
func WithHeader(name, value string) ClientOption {
	return func(c *Client) { c.header.Set(name, value) }
}

// WithHTTPClient supplies the HTTP client used for every request, for custom
// transports, proxies, or tests.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) { c.httpClient = httpClient }
}

// WithLogger sends SDK logs to a logger of your own, ignoring
// TYPESAFE_LOG_LEVEL. Request summaries are logged at info, headers and bodies
// at debug. Credential headers are redacted; bodies are not.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) { c.logger = logger }
}

// WithUnsafeDebugBodyLogging includes request and response bodies in debug logs.
// It is intended only for local troubleshooting because state can contain
// sensitive user data.
func WithUnsafeDebugBodyLogging() ClientOption {
	return func(c *Client) { c.logBodies = true }
}

// New creates a client for the TypeSafe AI API.
//
// It fails when no API key is available, when the configuration is invalid, or
// when TYPESAFE_LOG_LEVEL names a level that does not exist.
func New(options ...ClientOption) (*Client, error) {
	client := &Client{header: http.Header{}}
	for _, option := range options {
		option(client)
	}

	client.apiKey = fromCodeOrEnv(client.apiKey, APIKeyEnv, "")
	if client.apiKey == "" {
		return nil, newError("no API key was provided; pass WithAPIKey or set the %s environment variable", APIKeyEnv)
	}
	client.baseURL = strings.TrimRight(fromCodeOrEnv(client.baseURL, BaseURLEnv, DefaultBaseURL), "/")
	if client.baseURL == "" {
		return nil, newError("the base URL must not be empty")
	}
	client.defaultModel = fromCodeOrEnv(client.defaultModel, DefaultModelEnv, DefaultModel)

	if client.timeout == 0 {
		client.timeout = DefaultTimeout
	}
	if client.timeout < 0 {
		return nil, newError("the timeout must be positive, got %s", client.timeout)
	}
	if !client.retrySet {
		client.retry = DefaultRetryPolicy()
	}
	if err := client.retry.validate(); err != nil {
		return nil, err
	}

	if client.httpClient == nil {
		client.httpClient = &http.Client{}
	}
	if client.logger == nil {
		logger, err := defaultLogger()
		if err != nil {
			return nil, err
		}
		client.logger = logger
	}

	client.Models = &Models{client: client}
	return client, nil
}

// BaseURL is the API root the client sends requests to.
func (c *Client) BaseURL() string { return c.baseURL }

// DefaultModel is the model used by requests that do not name one.
func (c *Client) DefaultModel() string { return c.defaultModel }

// RequestOption overrides a client setting for a single call.
type RequestOption func(*requestOptions)

type requestOptions struct {
	timeout   time.Duration
	retry     RetryPolicy
	header    http.Header
	model     string
	extraBody map[string]any
}

// WithRequestTimeout overrides the per-attempt timeout for one call.
func WithRequestTimeout(timeout time.Duration) RequestOption {
	return func(o *requestOptions) { o.timeout = timeout }
}

// WithRequestRetry overrides the retry policy for one call.
func WithRequestRetry(policy RetryPolicy) RequestOption {
	return func(o *requestOptions) { o.retry = policy }
}

// WithRequestHeader adds a header to one call, merged over the client's.
func WithRequestHeader(name, value string) RequestOption {
	return func(o *requestOptions) {
		if o.header == nil {
			o.header = http.Header{}
		}
		o.header.Set(name, value)
	}
}

// WithModel overrides the model for one [Client.SystemOne] call.
func WithModel(model string) RequestOption {
	return func(o *requestOptions) { o.model = model }
}

// WithExtraBody adds top-level fields to a [Client.SystemOne] request body,
// shallow-merged over it. A key that collides with state, model, or questions
// replaces it.
func WithExtraBody(fields map[string]any) RequestOption {
	return func(o *requestOptions) { o.extraBody = fields }
}

func (c *Client) resolve(options []RequestOption) (*requestOptions, error) {
	resolved := &requestOptions{timeout: c.timeout, retry: c.retry}
	for _, option := range options {
		option(resolved)
	}
	if resolved.timeout <= 0 {
		return nil, newError("the request timeout must be positive, got %s", resolved.timeout)
	}
	if err := resolved.retry.validate(); err != nil {
		return nil, err
	}
	return resolved, nil
}

// SystemOne answers named questions about text or structured state.
//
// state is the content every question refers to: a string, a map, or a slice.
// Answers come back keyed by the names the questions were given.
//
//	resp, err := client.SystemOne(ctx, "I was charged twice. Please help.",
//		typesafe.Questions{
//			"billing": typesafe.Noul{Instructions: "Is this about billing?"},
//			"tone": typesafe.Choice{
//				Instructions: "What is the tone?",
//				Criteria:     map[string]typesafe.Content{"calm": nil, "angry": nil},
//			},
//		})
//
// See https://docs.typesafe.ai/concepts/system-one for details.
func (c *Client) SystemOne(ctx context.Context, state Content, questions Questions, options ...RequestOption) (*SystemOneResponse, error) {
	if err := questions.Validate(); err != nil {
		return nil, err
	}
	resolved, err := c.resolve(options)
	if err != nil {
		return nil, err
	}

	model := resolved.model
	if model == "" {
		model = c.defaultModel
	}
	body := map[string]any{"state": state, "model": model, "questions": questions}
	for name, value := range resolved.extraBody {
		body[name] = value
	}

	resp, err := c.send(ctx, http.MethodPost, systemOnePath, body, resolved)
	if err != nil {
		return nil, err
	}
	return resp.decodeSystemOne()
}

// requestHeader layers the SDK's own headers over the caller's, so user headers
// can never clobber authentication or the JSON content type.
func (c *Client) requestHeader(extra http.Header, hasBody bool) http.Header {
	header := c.header.Clone()
	if header == nil {
		header = http.Header{}
	}
	for name, values := range extra {
		header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	header.Del(retryCountHeader)
	header.Set("Authorization", "Bearer "+c.apiKey)
	header.Set("Accept", jsonContentType)
	header.Set("User-Agent", sdkName+"/"+Version)
	header.Set(sdkHeader, sdkName+"/"+Version)
	header.Set(runtimeHeader, runtimeDescription)
	if hasBody {
		header.Set("Content-Type", jsonContentType)
	}
	return header
}
