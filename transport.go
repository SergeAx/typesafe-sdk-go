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

func (c *Client) send(ctx context.Context, method, path string, payload any, o *requestOptions) (*response, error) {
	var body []byte
	if payload != nil {
		encoded, err := encodeJSON(payload)
		if err != nil {
			return nil, &Error{Message: "the request body could not be encoded as JSON", Err: err}
		}
		body = encoded
	}

	url := c.baseURL + path
	endpoint := method + " " + url
	header := c.requestHeader(o.header, body != nil)
	tag := fmt.Sprintf("#%d %s %s", c.requests.Add(1), method, path)

	for attempt := 0; ; attempt++ {
		retriesLeft := o.retry.MaxRetries - attempt
		attemptHeader := header
		if attempt > 0 {
			attemptHeader = header.Clone()
			attemptHeader.Set(retryCountHeader, strconv.Itoa(attempt))
		}
		if c.logBodies {
			c.logger.Debug(tag+" request", "url", url, "headers", redactHeader(attemptHeader), "body", string(body))
		} else {
			c.logger.Debug(tag+" request", "url", url, "headers", redactHeader(attemptHeader), "body_omitted", true)
		}

		started := time.Now()
		resp, raw, err := c.attempt(ctx, method, url, body, attemptHeader, o.timeout)
		if err != nil {
			c.logger.Info(tag+" failed", "after", time.Since(started), "error", err)
			if retriesLeft <= 0 || !o.retry.retriesError(err) {
				return nil, err
			}
			if err := c.backOff(ctx, tag, attempt, retriesLeft, err.Error(), nil, o.retry); err != nil {
				return nil, err
			}
			continue
		}

		requestID := resp.Header.Get(requestIDHeader)
		c.logger.Info(tag+" response", "status", resp.StatusCode, "in", time.Since(started), "request_id", requestID)
		if c.logBodies {
			c.logger.Debug(tag+" response body", "headers", redactHeader(resp.Header), "body", string(raw))
		} else {
			c.logger.Debug(tag+" response body", "headers", redactHeader(resp.Header), "body_omitted", true)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return &response{http: resp, body: raw, endpoint: endpoint, requestID: requestID, logger: c.logger}, nil
		}

		apiErr := newAPIError(resp.StatusCode, resp.Header, decodeBody(raw), endpoint)
		if retriesLeft <= 0 || !o.retry.retriesStatus(resp.StatusCode) {
			return nil, apiErr
		}
		if err := c.backOff(ctx, tag, attempt, retriesLeft, strconv.Itoa(resp.StatusCode), resp.Header, o.retry); err != nil {
			return nil, err
		}
	}
}

// attempt performs one round trip, reading the whole body under the per-attempt
// timeout so a body that stops arriving is retried like any connection failure.
func (c *Client) attempt(ctx context.Context, method, url string, body []byte, header http.Header, timeout time.Duration) (*http.Response, []byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(attemptCtx, method, url, reader)
	if err != nil {
		return nil, nil, &Error{Message: "the request could not be built", Err: err}
	}
	req.Header = header
	if body != nil {
		req.ContentLength = int64(len(body))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, transportError(ctx, attemptCtx, timeout, err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, transportError(ctx, attemptCtx, timeout, err)
	}
	return resp, raw, nil
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

func (c *Client) backOff(ctx context.Context, tag string, attempt, retriesLeft int, reason string, header http.Header, policy RetryPolicy) error {
	delay := policy.delay(attempt, header, time.Now())
	c.logger.Info(tag+" retrying", "in", delay, "retry", attempt+1, "of", attempt+retriesLeft, "after", reason)

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("typesafe: request canceled while waiting to retry: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
