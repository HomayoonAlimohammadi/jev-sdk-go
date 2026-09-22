package jev

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	retryAfterHeader   = "Retry-After"
	retryAfterMsHeader = "Retry-After-Ms"
)

// RetryPolicy controls how a call is retried.
//
// The zero value performs no retries at all. Start from [DefaultRetryPolicy]
// and adjust from there; individual zero fields are used as written, because a
// zero BackoffInitial is a meaningful "no backoff" setting.
type RetryPolicy struct {
	// MaxRetries is how many attempts are made after the first one. Zero
	// disables retries.
	MaxRetries int

	// BackoffInitial is the delay before the first retry. It doubles each
	// attempt up to BackoffMax. Zero disables backoff.
	BackoffInitial time.Duration

	// BackoffMax caps the backoff delay. Zero disables backoff.
	BackoffMax time.Duration

	// BackoffJitter is the fraction of each delay, between 0 and 1, that is
	// randomly subtracted to spread retries out.
	BackoffJitter float64

	// RetryStatuses lists the response status codes that are retried.
	RetryStatuses []int

	// RespectRetryAfter honors the retry-after and retry-after-ms response
	// headers in place of the computed backoff.
	RespectRetryAfter bool

	// RetryTransport retries failures that produced no HTTP response, such as
	// connection errors and timeouts.
	RetryTransport bool

	// Budget caps the total wall-clock time of one call, including the first
	// attempt and every delay. A retry whose delay would reach the budget is
	// not made, and the last error is returned instead. Zero disables the cap.
	Budget time.Duration

	// Retry opts additional failures in, on top of the rules above. It is
	// called with the API error for a response, or the transport error for a
	// failed attempt; exactly one is non-nil.
	Retry func(apiErr *APIError, err error) bool
}

// DefaultRetryPolicy returns the policy a client uses when none is set: two
// retries with exponential backoff from 500ms to 5s, honoring Retry-After,
// within a 30s budget.
func DefaultRetryPolicy() RetryPolicy {
	statuses := []int{http.StatusRequestTimeout, http.StatusTooManyRequests}
	for status := 500; status < 600; status++ {
		statuses = append(statuses, status)
	}

	return RetryPolicy{
		MaxRetries:        2,
		BackoffInitial:    500 * time.Millisecond,
		BackoffMax:        5 * time.Second,
		BackoffJitter:     0.25,
		RetryStatuses:     statuses,
		RespectRetryAfter: true,
		RetryTransport:    true,
		Budget:            30 * time.Second,
	}
}

func (p RetryPolicy) validate() error {
	if p.MaxRetries < 0 {
		return fmt.Errorf("%w: MaxRetries must not be negative", ErrInvalidRetry)
	}
	if p.BackoffInitial < 0 {
		return fmt.Errorf("%w: BackoffInitial must not be negative", ErrInvalidRetry)
	}
	if p.BackoffMax < 0 {
		return fmt.Errorf("%w: BackoffMax must not be negative", ErrInvalidRetry)
	}
	if p.BackoffJitter < 0 || p.BackoffJitter > 1 || math.IsNaN(p.BackoffJitter) {
		return fmt.Errorf("%w: BackoffJitter must be between 0 and 1", ErrInvalidRetry)
	}
	if p.Budget < 0 {
		return fmt.Errorf("%w: Budget must not be negative", ErrInvalidRetry)
	}
	return nil
}

// retryable reports whether a failed attempt should be tried again. Exactly
// one of apiErr and err is non-nil.
func (p RetryPolicy) retryable(apiErr *APIError, err error) bool {
	switch {
	case apiErr != nil:
		for _, status := range p.RetryStatuses {
			if status == apiErr.StatusCode {
				return true
			}
		}
	case err != nil:
		if p.RetryTransport {
			return true
		}
	}
	return p.Retry != nil && p.Retry(apiErr, err)
}

// backoff returns the delay before the retry following attempt n, counting
// from 1. random must be in [0, 1); it is the jitter draw.
func (p RetryPolicy) backoff(attempt int, random float64) time.Duration {
	if p.BackoffInitial <= 0 || p.BackoffMax <= 0 {
		return 0
	}

	delay := p.BackoffInitial
	for range max(attempt-1, 0) {
		if delay >= p.BackoffMax || delay > math.MaxInt64/2 {
			break
		}
		delay *= 2
	}
	delay = min(delay, p.BackoffMax)

	return delay - time.Duration(float64(delay)*p.BackoffJitter*random)
}

// delay returns how long to wait before the retry following attempt n. A
// server-requested delay wins over the computed backoff, however long it is;
// the retry budget is what bounds an unreasonable wait.
func (p RetryPolicy) delay(attempt int, header http.Header, random float64, now time.Time) time.Duration {
	if p.RespectRetryAfter && header != nil {
		if requested, ok := parseRetryAfter(header, now); ok {
			return requested
		}
	}
	return p.backoff(attempt, random)
}

// parseRetryAfter reads the server's requested delay, preferring the
// millisecond header. It reports false when no usable delay was given, in
// which case the caller falls back to its own backoff.
func parseRetryAfter(header http.Header, now time.Time) (time.Duration, bool) {
	units := [...]struct {
		name string
		unit float64
	}{
		{retryAfterMsHeader, float64(time.Millisecond)},
		{retryAfterHeader, float64(time.Second)},
	}

	for _, candidate := range units {
		values := header.Values(candidate.name)
		if len(values) == 0 {
			continue
		}

		raw := strings.TrimSpace(values[0])
		if raw == "" {
			raw = "0"
		}

		seconds, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			// Only the seconds header may carry an HTTP date instead.
			if candidate.name == retryAfterHeader {
				if deadline, err := http.ParseTime(values[0]); err == nil {
					return max(deadline.Sub(now), 0), true
				}
			}
			continue
		}

		if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			continue
		}
		if seconds < 0 {
			// A negative millisecond value defers to the seconds header; a
			// negative seconds value means the server asked for nothing.
			if candidate.name == retryAfterHeader {
				return 0, false
			}
			continue
		}

		delay := seconds * candidate.unit
		if math.IsInf(delay, 0) || delay > math.MaxInt64 {
			continue
		}
		return time.Duration(delay), true
	}

	return 0, false
}
