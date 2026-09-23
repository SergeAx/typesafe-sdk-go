package typesafe

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Failure categories, for matching with [errors.Is]:
//
//	if errors.Is(err, typesafe.ErrRateLimit) { ... }
//
// A caller-canceled request instead matches [context.Canceled], and a request
// whose context deadline expires matches [context.DeadlineExceeded].
var (
	ErrBadRequest          = errors.New("typesafe: bad request")
	ErrAuthentication      = errors.New("typesafe: authentication failed")
	ErrPermissionDenied    = errors.New("typesafe: permission denied")
	ErrNotFound            = errors.New("typesafe: not found")
	ErrUnprocessableEntity = errors.New("typesafe: unprocessable entity")
	ErrRateLimit           = errors.New("typesafe: rate limit exceeded")
	ErrInternalServer      = errors.New("typesafe: internal server error")
	ErrConnection          = errors.New("typesafe: connection error")
	ErrTimeout             = errors.New("typesafe: request timed out")
)

const maxErrorBodyLength = 200

// Error reports a failure raised before the request reached the API: invalid
// configuration, invalid questions, or a body that could not be encoded.
type Error struct {
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return "typesafe: " + e.Message
	}
	return "typesafe: " + e.Message + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func newError(format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

// APIError is an unsuccessful HTTP response from the API. Use [errors.AsType]
// to reach the status and body, and [errors.Is] with one of the category
// sentinels to test for a specific failure.
type APIError struct {
	// Status is the HTTP response status code.
	Status int
	Header http.Header
	// Body is the decoded JSON body, the response text when the body is not
	// JSON, or nil when the response had no body.
	Body any
	// RequestID is the x-typesafe-request-id response header, empty when absent.
	RequestID string
	// Endpoint is the request method and URL, without query or fragment.
	Endpoint string
	// Message is the server's explanation, or a truncated body when it gave none.
	Message string
}

func newAPIError(status int, header http.Header, body any, endpoint string) *APIError {
	return &APIError{
		Status:    status,
		Header:    header,
		Body:      body,
		RequestID: header.Get(requestIDHeader),
		Endpoint:  endpoint,
		Message:   describeFailure(body),
	}
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("typesafe: ")
	if e.Endpoint != "" {
		b.WriteString(e.Endpoint)
		b.WriteString(": ")
	}
	fmt.Fprintf(&b, "%d", e.Status)
	if e.Message != "" {
		b.WriteString(" ")
		b.WriteString(e.Message)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (request_id=%s)", e.RequestID)
	}
	return b.String()
}

func (e *APIError) Is(target error) bool {
	switch target {
	case ErrBadRequest:
		return e.Status == http.StatusBadRequest
	case ErrAuthentication:
		return e.Status == http.StatusUnauthorized
	case ErrPermissionDenied:
		return e.Status == http.StatusForbidden
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrUnprocessableEntity:
		return e.Status == http.StatusUnprocessableEntity
	case ErrRateLimit:
		return e.Status == http.StatusTooManyRequests
	case ErrInternalServer:
		return e.Status >= http.StatusInternalServerError
	}
	return false
}

// RetryAfter reports the delay the server asked for, taken from the
// retry-after-ms or Retry-After response header.
func (e *APIError) RetryAfter() (time.Duration, bool) {
	return parseRetryAfter(e.Header, time.Now())
}

// ResponseValidationError is a successful HTTP response whose body was missing
// required data or did not match the documented schema.
type ResponseValidationError struct {
	// Status is the HTTP response status code.
	Status int
	Header http.Header
	// Body is the decoded JSON body, or the response text when it is not JSON.
	Body any
	// Field is the dotted path to the offending value, such as
	// "answers.tone.confidence", or empty when the body is not JSON at all.
	Field string
	// RequestID is the x-typesafe-request-id response header, empty when absent.
	RequestID string
	// Endpoint is the request method and URL, without query or fragment.
	Endpoint string
	Err      error
}

func (e *ResponseValidationError) Error() string {
	var b strings.Builder
	b.WriteString("typesafe: ")
	if e.Endpoint != "" {
		b.WriteString(e.Endpoint)
		b.WriteString(": ")
	}
	if e.Field == "" {
		b.WriteString("the response body is not valid JSON")
	} else {
		fmt.Fprintf(&b, "invalid response data at %q", e.Field)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (request_id=%s)", e.RequestID)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func (e *ResponseValidationError) Unwrap() error { return e.Err }

// ConnectionError is a request that failed without producing an HTTP response:
// DNS, TLS, a refused connection, or a body that stopped arriving midway.
type ConnectionError struct {
	Message string
	Err     error
}

func (e *ConnectionError) Error() string { return "typesafe: " + e.Message }

func (e *ConnectionError) Unwrap() error { return e.Err }

func (e *ConnectionError) Is(target error) bool { return target == ErrConnection }

// TimeoutError is a request attempt that exceeded its per-attempt timeout. It
// matches both [ErrTimeout] and [ErrConnection].
type TimeoutError struct {
	// Timeout is the per-attempt timeout that elapsed.
	Timeout time.Duration
	Err     error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("typesafe: request timed out after %s", e.Timeout)
}

func (e *TimeoutError) Unwrap() error { return e.Err }

func (e *TimeoutError) Is(target error) bool {
	return target == ErrTimeout || target == ErrConnection
}

// describeFailure summarizes an error body, falling back to the raw body when
// the server did not name a reason. Error pages can be arbitrarily long, so
// every path is truncated.
func describeFailure(body any) string {
	if message := extractMessage(body); message != "" {
		return truncate(message)
	}
	if body == nil {
		return "status code (no body)"
	}
	encoded, err := encodeJSON(body)
	if err != nil {
		return "status code (unreadable body)"
	}
	return truncate(string(encoded))
}

func truncate(text string) string {
	if len(text) <= maxErrorBodyLength {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxErrorBodyLength {
		return text
	}
	return string(runes[:maxErrorBodyLength]) + "…"
}

func extractMessage(body any) string {
	switch v := body.(type) {
	case string:
		return v
	case map[string]any:
		if s, ok := v["error"].(string); ok && s != "" {
			return s
		}
		if nested, ok := v["error"].(map[string]any); ok {
			if s, ok := nested["message"].(string); ok && s != "" {
				return s
			}
		}
		if s, ok := v["message"].(string); ok && s != "" {
			return s
		}
		switch detail := v["detail"].(type) {
		case string:
			return detail
		case map[string]any:
			if s, ok := detail["message"].(string); ok {
				return s
			}
		case []any:
			return describeValidationErrors(detail)
		}
	}
	return ""
}

// describeValidationErrors formats a 422 body as semicolon-separated
// "path: message" entries.
func describeValidationErrors(entries []any) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		msg, ok := fields["msg"].(string)
		if !ok {
			continue
		}
		path := ""
		if loc, ok := fields["loc"].([]any); ok {
			segments := make([]string, 0, len(loc))
			for _, item := range loc {
				if s, ok := item.(string); ok && s == "body" {
					continue
				}
				segments = append(segments, fmt.Sprint(item))
			}
			path = strings.Join(segments, ".")
		}
		if path == "" {
			parts = append(parts, msg)
		} else {
			parts = append(parts, path+": "+msg)
		}
	}
	return strings.Join(parts, "; ")
}
