package typesafe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// request is what every attempt of one call sends.
type request struct {
	method, url, tag string
	header           http.Header
	body             []byte
	timeout          time.Duration
}

func (c *Client) send(ctx context.Context, method, path string, payload any, o *requestOptions) (*response, error) {
	var body []byte
	if payload != nil {
		encoded, err := encodeJSON(payload)
		if err != nil {
			return nil, &Error{Message: "the request body could not be encoded as JSON", Err: err}
		}
		body = encoded
	}
	r := request{
		method:  method,
		url:     c.baseURL + path,
		tag:     fmt.Sprintf("#%d %s %s", c.requests.Add(1), method, path),
		header:  c.requestHeader(o.header, body != nil),
		body:    body,
		timeout: o.timeout,
	}

	for attempt := 0; ; attempt++ {
		resp, err := c.attempt(ctx, r, attempt)
		if err == nil {
			return resp, nil
		}
		if attempt >= o.retry.MaxRetries || !o.retry.retries(err) {
			return nil, err
		}
		if err := c.backOff(ctx, r.tag, attempt, err, o.retry); err != nil {
			return nil, err
		}
	}
}

// attempt sends the request once, reading the whole body under the per-attempt
// timeout so a body that stops arriving is retried like any connection failure.
// An unsuccessful status comes back as an *APIError.
func (c *Client) attempt(ctx context.Context, r request, attempt int) (*response, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	var reader io.Reader
	if r.body != nil {
		reader = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(attemptCtx, r.method, r.url, reader)
	if err != nil {
		return nil, &Error{Message: "the request could not be built", Err: err}
	}
	req.Header = r.header
	if attempt > 0 {
		req.Header = r.header.Clone()
		req.Header.Set(retryCountHeader, strconv.Itoa(attempt))
	}
	if r.body != nil {
		req.ContentLength = int64(len(r.body))
	}
	c.logger.Debug(r.tag+" request", "url", r.url, "headers", redactHeader(req.Header), "body", string(r.body))

	started := time.Now()
	resp, err := c.httpClient.Do(req)
	var raw []byte
	if err == nil {
		raw, err = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if err != nil {
		err = transportError(ctx, attemptCtx, r.timeout, err)
		c.logger.Info(r.tag+" failed", "after", time.Since(started), "error", err)
		return nil, err
	}
	requestID := resp.Header.Get(requestIDHeader)
	c.logger.Info(r.tag+" response", "status", resp.StatusCode, "in", time.Since(started), "request_id", requestID)
	c.logger.Debug(r.tag+" body", "headers", redactHeader(resp.Header), "body", string(raw))

	endpoint := r.method + " " + r.url
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newAPIError(resp.StatusCode, resp.Header, decodeBody(raw), endpoint)
	}
	return &response{http: resp, body: raw, endpoint: endpoint, requestID: requestID, logger: c.logger}, nil
}

// transportError tells the three ways an attempt can fail apart: the caller gave
// up, the per-attempt timeout elapsed, or the connection itself failed.
func transportError(ctx, attemptCtx context.Context, timeout time.Duration, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("typesafe: request canceled: %w", ctxErr)
	}
	if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return &TimeoutError{Timeout: timeout, Err: err}
	}
	return &ConnectionError{Message: "connection error: " + err.Error(), Err: err}
}

func (c *Client) backOff(ctx context.Context, tag string, attempt int, err error, policy RetryPolicy) error {
	delay := policy.delay(attempt, err)
	c.logger.Info(tag+" retrying", "in", delay, "retry", attempt+1, "of", policy.MaxRetries, "after", err)

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("typesafe: request canceled while waiting to retry: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
