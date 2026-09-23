# TypeSafe AI Go SDK

Go SDK for [TypeSafe AI](https://typesafe.ai).

## Quickstart

Install the SDK (Go 1.26 or newer):

```sh
go get serge.ax/go/typesafe-sdk-go
```

Set `TYPESAFE_API_KEY` in your environment, then create and use the client:

```go
package main

import (
	"context"
	"fmt"
	"log"

	typesafe "serge.ax/go/typesafe-sdk-go"
)

func main() {
	client, err := typesafe.New()
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.SystemOne(context.Background(),
		"I was charged twice. Please fix this ASAP.",
		typesafe.Questions{
			"category": typesafe.Choice{
				Instructions: "What is this ticket about?",
				Criteria: map[string]typesafe.Content{
					"billing": nil, "technical": nil, "other": nil,
				},
			},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Choices()["category"].Choice)
}
```

The import path and the package name differ, so the `typesafe` alias above is
worth keeping for readability.

## Questions

Mix question types in one request; each answer comes back under the name you
gave its question.

```go
result, err := client.SystemOne(ctx,
	map[string]any{"subject": "Duplicate charge", "message": "I was charged twice."},
	typesafe.Questions{
		"billing": typesafe.Noul{Instructions: "Is this about billing?"},
		"tone": typesafe.Choice{
			Instructions: "What is the tone of this message?",
			Criteria:     map[string]typesafe.Content{"angry": "Upset or hostile", "calm": "Neutral or polite"},
		},
		"urgency": typesafe.Score{
			Instructions: "How urgent is this message?",
			Criteria:     []typesafe.Content{"Can wait", "Needs attention this week", "Needs attention today"},
		},
	},
)

result.Nouls()["billing"].Noul       // probability of yes, 0 to 1
result.Choices()["tone"].Choice      // "angry" or "calm"
result.Scores()["urgency"].Score     // expected score, may fall between levels
```

| Question | Answer | Fields |
| --- | --- | --- |
| [`Noul`](https://docs.typesafe.ai/primitives/noul) — yes/no | `*NoulAnswer` | `Noul` |
| [`Choice`](https://docs.typesafe.ai/primitives/choice) — pick a label | `*ChoiceAnswer` | `Choice`, `Confidence`, `Probabilities` |
| [`Score`](https://docs.typesafe.ai/primitives/score) — rate against a rubric | `*ScoreAnswer` | `Score`, `Confidence`, `Legend`, `Probabilities` |

`Instructions` and every criterion take a string, a `map[string]any`, a
`[]any`, or `nil`. Answers of a type this SDK version does not model are
dropped with a warning, so a newer API never breaks an older client; reach the
raw payload through `result.HTTPResponse`.

## Configuration

Options take precedence over environment variables, which take precedence over
the SDK defaults. Empty or whitespace-only environment values are ignored.

| Option | Environment | Default |
| --- | --- | --- |
| `WithAPIKey` | `TYPESAFE_API_KEY` | required |
| `WithBaseURL` | `TYPESAFE_BASE_URL` | `https://api.typesafe.ai` |
| `WithDefaultModel` | `TYPESAFE_DEFAULT_MODEL` | `jev-latest` |
| `WithLogger` | `TYPESAFE_LOG_LEVEL` | `warn`, to standard error |
| `WithTimeout` | — | 10s per attempt |
| `WithRetry` | — | see below |
| `WithHeader`, `WithHTTPClient` | — | — |

Per-call overrides: `WithModel`, `WithRequestTimeout`, `WithRequestRetry`,
`WithRequestHeader`, `WithExtraBody`.

A `*Client` is safe for concurrent use.

## Retries

By default a failed request is retried twice, with exponential backoff from
500ms to 5s minus up to 25% jitter, for HTTP 408, 429 and 5xx responses as well
as connection errors and timeouts. `Retry-After` and `retry-after-ms` are
honored up to a minute.

```go
retry := typesafe.DefaultRetryPolicy()
retry.MaxRetries = 5
retry.RetryStatus = func(status int) bool { return status == 429 || status >= 500 }

client, err := typesafe.New(typesafe.WithRetry(retry))
```

Pass a zero `RetryPolicy` to disable retries. There is no separate retry
budget: the timeout applies per attempt, and a context deadline bounds the call
as a whole, backoff included.

## Errors

```go
result, err := client.SystemOne(ctx, state, questions)
switch {
case errors.Is(err, typesafe.ErrRateLimit):
	apiErr, _ := errors.AsType[*typesafe.APIError](err)
	delay, ok := apiErr.RetryAfter()
case errors.Is(err, typesafe.ErrTimeout):
	// the attempt exceeded its timeout
case errors.Is(err, context.Canceled):
	// the caller gave up
}
```

- `*Error` — bad configuration or invalid questions, raised before anything is sent.
- `*APIError` — an unsuccessful response, carrying `Status`, `Header`, `Body`,
  `RequestID`, and `Endpoint`. Matches `ErrBadRequest`, `ErrAuthentication`,
  `ErrPermissionDenied`, `ErrNotFound`, `ErrUnprocessableEntity`,
  `ErrRateLimit`, or `ErrInternalServer`.
- `*ResponseValidationError` — a successful response whose body was missing
  required data, naming the offending `Field`.
- `*ConnectionError` and `*TimeoutError` — the request never produced a usable
  response. A timeout matches both `ErrTimeout` and `ErrConnection`.
- A canceled or expired context matches `context.Canceled` or
  `context.DeadlineExceeded`.

## Logging

The SDK logs through `log/slog`: request summaries at info, headers and bodies
at debug. Credential headers are redacted; bodies are not. Set
`TYPESAFE_LOG_LEVEL` (`debug`, `info`, `warn`, `error`, `off`), or pass your own
logger with `WithLogger`.

## Documentation

Learn what TypeSafe can do in the [TypeSafe docs](https://docs.typesafe.ai/).
The API reference for this package is on
[pkg.go.dev](https://pkg.go.dev/serge.ax/go/typesafe-sdk-go).

Sibling SDKs: [JavaScript](https://github.com/typesafe-ai/typesafe-sdk-js),
[Python](https://github.com/typesafe-ai/typesafe-sdk-python).

## License

[MIT](LICENSE)
