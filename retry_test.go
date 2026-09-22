package jev

import (
	"errors"
	"math"
	"net/http"
	"testing"
	"time"
)

func header(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Add(pairs[i], pairs[i+1])
	}
	return h
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000_000, 0)
	future := now.Add(10 * time.Second).UTC().Format(http.TimeFormat)
	past := now.Add(-10 * time.Second).UTC().Format(http.TimeFormat)

	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
		wantOK bool
	}{
		{"absent", header(), 0, false},
		{"seconds", header("Retry-After", "2"), 2 * time.Second, true},
		{"fractional seconds", header("Retry-After", "1.5"), 1500 * time.Millisecond, true},
		{"milliseconds", header("Retry-After-Ms", "125"), 125 * time.Millisecond, true},
		{"milliseconds win", header("Retry-After-Ms", "0", "Retry-After", "50"), 0, true},
		{"long delay honored", header("Retry-After", "60"), 60 * time.Second, true},

		{"unparseable seconds", header("Retry-After", "bad"), 0, false},
		{"negative seconds", header("Retry-After", "-1"), 0, false},
		{"empty seconds", header("Retry-After", ""), 0, true},

		// A negative or unusable millisecond value defers to the seconds header.
		{"nan ms falls through", header("Retry-After-Ms", "NaN", "Retry-After", "1.5"), 1500 * time.Millisecond, true},
		{"negative ms falls through", header("Retry-After-Ms", "-1", "Retry-After", "2"), 2 * time.Second, true},
		{"bad ms falls through", header("Retry-After-Ms", "bad", "Retry-After", "2"), 2 * time.Second, true},
		{"infinite ms", header("Retry-After-Ms", "inf"), 0, false},
		{"overflowing seconds", header("Retry-After", "1e308"), 0, false},

		{"http date", header("Retry-After", future), 10 * time.Second, true},
		{"http date in the past", header("Retry-After", past), 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseRetryAfter(tt.header, now)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("parseRetryAfter() = (%v, %v), want (%v, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	t.Parallel()

	policy := DefaultRetryPolicy()

	// With no jitter draw the sequence doubles from BackoffInitial to BackoffMax.
	for attempt, want := range map[int]time.Duration{
		1:  500 * time.Millisecond,
		2:  time.Second,
		3:  2 * time.Second,
		4:  4 * time.Second,
		5:  5 * time.Second,
		20: 5 * time.Second,
	} {
		if got := policy.backoff(attempt, 0); got != want {
			t.Errorf("backoff(%d, 0) = %v, want %v", attempt, got, want)
		}
	}

	// A full jitter draw subtracts the whole jitter fraction.
	if got, want := policy.backoff(1, 1), 375*time.Millisecond; got != want {
		t.Errorf("backoff(1, 1) = %v, want %v", got, want)
	}
	if got, want := policy.backoff(2, 0.5), 875*time.Millisecond; got != want {
		t.Errorf("backoff(2, 0.5) = %v, want %v", got, want)
	}
}

func TestBackoffEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		policy  RetryPolicy
		attempt int
		want    time.Duration
	}{
		{"zero initial", RetryPolicy{BackoffInitial: 0, BackoffMax: 5 * time.Second}, 1, 0},
		{"zero max", RetryPolicy{BackoffInitial: 500 * time.Millisecond, BackoffMax: 0}, 1, 0},
		{"both zero", RetryPolicy{}, 1, 0},
		{"max below initial", RetryPolicy{BackoffInitial: 500 * time.Millisecond, BackoffMax: time.Millisecond}, 1, time.Millisecond},
		{"huge attempt does not overflow", RetryPolicy{BackoffInitial: time.Nanosecond, BackoffMax: time.Hour}, 1 << 20, time.Hour},
		{"initial near max int", RetryPolicy{BackoffInitial: math.MaxInt64, BackoffMax: math.MaxInt64}, 5, math.MaxInt64},
		{"attempt zero", RetryPolicy{BackoffInitial: time.Second, BackoffMax: time.Minute}, 0, time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.policy.backoff(tt.attempt, 0); got != tt.want {
				t.Errorf("backoff(%d) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestDelayPrefersServerRequest(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000_000, 0)
	policy := DefaultRetryPolicy()

	if got, want := policy.delay(1, header("Retry-After", "5"), 0, now), 5*time.Second; got != want {
		t.Errorf("delay() = %v, want the server's %v", got, want)
	}
	// An unparseable header falls back to the computed backoff.
	if got, want := policy.delay(1, header("Retry-After", "bad"), 0, now), 500*time.Millisecond; got != want {
		t.Errorf("delay() = %v, want the computed %v", got, want)
	}
	if got, want := policy.delay(1, nil, 0, now), 500*time.Millisecond; got != want {
		t.Errorf("delay() = %v, want the computed %v", got, want)
	}

	ignoring := policy
	ignoring.RespectRetryAfter = false
	if got, want := ignoring.delay(1, header("Retry-After", "5"), 0, now), 500*time.Millisecond; got != want {
		t.Errorf("delay() = %v, want the computed %v", got, want)
	}
}

func TestRetryable(t *testing.T) {
	t.Parallel()

	transport := errors.New("connection refused")

	tests := []struct {
		name   string
		policy RetryPolicy
		apiErr *APIError
		err    error
		want   bool
	}{
		{"retried status", DefaultRetryPolicy(), &APIError{StatusCode: 429}, nil, true},
		{"retried 5xx", DefaultRetryPolicy(), &APIError{StatusCode: 503}, nil, true},
		{"retried 408", DefaultRetryPolicy(), &APIError{StatusCode: 408}, nil, true},
		{"not retried 400", DefaultRetryPolicy(), &APIError{StatusCode: 400}, nil, false},
		{"not retried 404", DefaultRetryPolicy(), &APIError{StatusCode: 404}, nil, false},
		{"not retried 409", DefaultRetryPolicy(), &APIError{StatusCode: 409}, nil, false},
		{"transport", DefaultRetryPolicy(), nil, transport, true},
		{"transport disabled", RetryPolicy{}, nil, transport, false},
		{"custom statuses", RetryPolicy{RetryStatuses: []int{409}}, &APIError{StatusCode: 409}, nil, true},
		{"custom statuses exclude", RetryPolicy{RetryStatuses: []int{409}}, &APIError{StatusCode: 500}, nil, false},
		{
			"predicate opts in",
			RetryPolicy{Retry: func(apiErr *APIError, _ error) bool { return apiErr != nil && apiErr.StatusCode == 404 }},
			&APIError{StatusCode: 404}, nil, true,
		},
		{
			"predicate not consulted when already retryable",
			RetryPolicy{RetryStatuses: []int{429}, Retry: func(*APIError, error) bool { panic("should not be called") }},
			&APIError{StatusCode: 429}, nil, true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.policy.retryable(tt.apiErr, tt.err); got != tt.want {
				t.Errorf("retryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryPolicyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		policy  RetryPolicy
		wantErr bool
	}{
		{"default", DefaultRetryPolicy(), false},
		{"zero", RetryPolicy{}, false},
		{"negative retries", RetryPolicy{MaxRetries: -1}, true},
		{"negative initial", RetryPolicy{BackoffInitial: -1}, true},
		{"negative max", RetryPolicy{BackoffMax: -1}, true},
		{"negative jitter", RetryPolicy{BackoffJitter: -0.1}, true},
		{"jitter above one", RetryPolicy{BackoffJitter: 1.1}, true},
		{"nan jitter", RetryPolicy{BackoffJitter: math.NaN()}, true},
		{"negative budget", RetryPolicy{Budget: -1}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.policy.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidRetry) {
				t.Errorf("validate() error = %v, want it to wrap ErrInvalidRetry", err)
			}
		})
	}
}

func FuzzParseRetryAfter(f *testing.F) {
	for _, seed := range []string{"2", "-1", "bad", "", "1e308", "NaN", http.TimeFormat} {
		f.Add(seed, seed)
	}

	f.Fuzz(func(t *testing.T, ms, seconds string) {
		got, ok := parseRetryAfter(header(retryAfterMsHeader, ms, retryAfterHeader, seconds), time.Unix(0, 0))
		if ok && got < 0 {
			t.Fatalf("parseRetryAfter() = %v, want a non-negative delay", got)
		}
	})
}

func TestMaxRetryAfter(t *testing.T) {
	t.Parallel()

	always := func(*APIError, error) bool { return true }

	tests := []struct {
		name       string
		policy     RetryPolicy
		retryAfter time.Duration
		want       bool
	}{
		{"under the cap", RetryPolicy{RetryStatuses: []int{429}, RespectRetryAfter: true, MaxRetryAfter: time.Minute}, 30 * time.Second, true},
		{"at the cap", RetryPolicy{RetryStatuses: []int{429}, RespectRetryAfter: true, MaxRetryAfter: time.Minute}, time.Minute, true},
		// The server asked for longer than the caller will wait: return now,
		// with the request in RetryAfter, rather than hammering it with backoff.
		{"over the cap", RetryPolicy{RetryStatuses: []int{429}, RespectRetryAfter: true, MaxRetryAfter: time.Minute}, 2 * time.Minute, false},
		{"no cap", RetryPolicy{RetryStatuses: []int{429}, RespectRetryAfter: true}, time.Hour, true},
		{"header ignored, so no cap", RetryPolicy{RetryStatuses: []int{429}, MaxRetryAfter: time.Minute}, time.Hour, true},
		{"predicate cannot override the cap", RetryPolicy{RespectRetryAfter: true, MaxRetryAfter: time.Minute, Retry: always}, time.Hour, false},
		{"no header at all", RetryPolicy{RetryStatuses: []int{429}, RespectRetryAfter: true, MaxRetryAfter: time.Minute}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			apiErr := &APIError{StatusCode: 429, RetryAfter: tt.retryAfter}
			if got := tt.policy.retryable(apiErr, nil); got != tt.want {
				t.Errorf("retryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultRetryPolicyCapsRetryAfter(t *testing.T) {
	t.Parallel()

	if got := DefaultRetryPolicy().MaxRetryAfter; got != time.Minute {
		t.Errorf("DefaultRetryPolicy().MaxRetryAfter = %v, want 1m", got)
	}
	if err := (RetryPolicy{MaxRetryAfter: -1}).validate(); !errors.Is(err, ErrInvalidRetry) {
		t.Errorf("validate() error = %v, want ErrInvalidRetry for a negative cap", err)
	}
}
