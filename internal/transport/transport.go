// Package transport moves bytes for the SDK. It builds and sends HTTP
// requests, replays them according to a retry policy, and redacts credentials
// from what it logs. It knows nothing about the API's domain types.
package transport

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// Request is one HTTP request. Its body is already encoded, so an attempt can
// be replayed without the caller re-encoding anything.
type Request struct {
	Method string
	URL    string

	// Endpoint labels the request in logs and errors. It is the method and
	// URL with any credentials removed.
	Endpoint string

	Header http.Header

	// Body is the request body, or nil for a request without one.
	Body []byte

	// Timeout bounds each attempt. Zero leaves the attempt bounded only by the
	// context and the underlying http.Client.
	Timeout time.Duration

	// RetryCountHeader, when set, carries the retry ordinal (1, 2, ...) on
	// every attempt after the first.
	RetryCountHeader string

	// RequestIDHeader, when set, names the response header to quote in logs.
	RequestIDHeader string
}

// Response is a completed exchange whose body has been read in full.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Policy decides whether and when an attempt is repeated.
type Policy struct {
	// MaxAttempts is the total number of attempts, including the first.
	// Values below one are treated as one.
	MaxAttempts int

	// Budget caps the wall-clock time of the whole call. A delay that would
	// reach it stops the loop instead. Zero disables the cap.
	Budget time.Duration

	// Retry reports whether the attempt should be repeated. Exactly one of
	// resp and err is non-nil.
	Retry func(resp *Response, err error) bool

	// Delay returns how long to wait before the retry following attempt n.
	Delay func(attempt int, resp *Response) time.Duration
}

// Client sends requests. Now and Sleep are injectable so retry timing can be
// tested without real time passing.
type Client struct {
	HTTP   *http.Client
	Logger *slog.Logger
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
}

// Sleep waits for d or until ctx is done, whichever comes first.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Send performs the request, repeating it while policy allows. It returns the
// last response, or the last transport error when no response was received,
// along with the number of attempts made. A cancelled context ends the loop
// immediately.
func (c *Client) Send(ctx context.Context, req Request, policy Policy) (*Response, int, error) {
	start := c.Now()
	limit := max(policy.MaxAttempts, 1)

	var (
		resp    *Response
		err     error
		attempt int
	)
	for attempt = 1; ; attempt++ {
		resp, err = c.attempt(ctx, req, attempt)

		// The caller giving up outranks any retry rule.
		if err != nil && ctx.Err() != nil {
			return nil, attempt, err
		}
		if attempt >= limit || !policy.Retry(resp, err) {
			break
		}

		delay := policy.Delay(attempt, resp)
		if policy.Budget > 0 && c.Now().Sub(start)+delay >= policy.Budget {
			break
		}

		c.Logger.Info("jev: retrying", "method", req.Method, "url", req.URL, "retry", attempt, "delay", delay)
		if err := c.Sleep(ctx, delay); err != nil {
			return nil, attempt, err
		}
	}

	return resp, attempt, err
}

func (c *Client) attempt(ctx context.Context, req Request, attempt int) (*Response, error) {
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	header := req.Header.Clone()
	if header == nil {
		header = http.Header{}
	}
	if req.RetryCountHeader != "" && attempt > 1 {
		header.Set(req.RetryCountHeader, strconv.Itoa(attempt-1))
	}

	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header = header
	if req.Body != nil {
		httpReq.ContentLength = int64(len(req.Body))
		httpReq.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(req.Body)), nil
		}
	}

	c.logRequest(ctx, req, header)

	started := c.Now()
	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		c.Logger.Info("jev: request failed", "method", req.Method, "url", req.URL, "error", err)
		return nil, err
	}
	defer httpResp.Body.Close()

	payload, err := io.ReadAll(httpResp.Body)
	if err != nil {
		c.Logger.Info("jev: response body unreadable", "method", req.Method, "url", req.URL, "error", err)
		return nil, err
	}

	c.logResponse(ctx, req, httpResp, payload, c.Now().Sub(started))
	return &Response{StatusCode: httpResp.StatusCode, Header: httpResp.Header, Body: payload}, nil
}
