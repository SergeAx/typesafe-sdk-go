package typesafe

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// response is one delivered HTTP response with its body already buffered, so a
// failed attempt can be retried and the body can be decoded more than once.
type response struct {
	http      *http.Response
	body      []byte
	endpoint  string
	requestID string
	logger    *slog.Logger
}

func (r *response) invalid(field string, err error) *ResponseValidationError {
	return &ResponseValidationError{
		Status:    r.http.StatusCode,
		Header:    r.http.Header,
		Body:      decodeBody(r.body),
		Field:     field,
		RequestID: r.requestID,
		Endpoint:  r.endpoint,
		Err:       err,
	}
}

// unmarshal decodes the value found at path, reporting a mistyped one at the
// field it sits in.
func (r *response) unmarshal(path string, raw []byte, into any) error {
	err := json.Unmarshal(raw, into)
	if err == nil {
		return nil
	}
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok && typeErr.Field != "" {
		path += "." + typeErr.Field
	}
	return r.invalid(path, err)
}
