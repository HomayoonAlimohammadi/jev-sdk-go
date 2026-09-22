package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a fake clock: time only moves when the test says so, and sleeps are
// recorded rather than performed.
type clock struct {
	mu     sync.Mutex
	now    time.Time
	delays []time.Duration
}

func newClock() *clock { return &clock{now: time.Unix(1_000_000, 0)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.delays = append(c.delays, d)
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return ctx.Err()
}

func (c *clock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}

func newClient(clk *clock) *Client {
	return &Client{
		HTTP:   &http.Client{},
		Logger: slog.New(slog.DiscardHandler),
		Now:    clk.Now,
		Sleep:  clk.Sleep,
	}
}

// alwaysRetry retries any outcome with a fixed delay.
func alwaysRetry(attempts int, delay time.Duration) Policy {
	return Policy{
		MaxAttempts: attempts,
		Retry:       func(*Response, error) bool { return true },
		Delay:       func(int, *Response) time.Duration { return delay },
	}
}

func request(url string) Request {
	return Request{
		Method:           http.MethodPost,
		URL:              url,
		Endpoint:         "POST " + url,
		Header:           http.Header{"X-Test": []string{"1"}},
		Body:             []byte(`{"state":"hi"}`),
		RetryCountHeader: "X-Retry-Count",
		RequestIDHeader:  "X-Request-Id",
	}
}

func TestSendRetriesAndReplaysBody(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		bodies   []string
		counters []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		counters = append(counters, r.Header.Get("X-Retry-Count"))
		mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	clk := newClock()
	resp, attempts, err := newClient(clk).Send(t.Context(), request(server.URL), alwaysRetry(3, time.Second))
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", resp.StatusCode)
	}
	if attempts != 3 {
		t.Errorf("Send() reported %d attempts, want 3", attempts)
	}

	if len(bodies) != 3 {
		t.Fatalf("made %d attempts, want 3", len(bodies))
	}
	for i, body := range bodies {
		if body != `{"state":"hi"}` {
			t.Errorf("attempt %d body = %q, want the request body replayed", i+1, body)
		}
	}
	// The first attempt carries no retry count; retries are numbered from one.
	if want := []string{"", "1", "2"}; !equalStrings(counters, want) {
		t.Errorf("retry counters = %v, want %v", counters, want)
	}
	if want := []time.Duration{time.Second, time.Second}; !equalDurations(clk.recorded(), want) {
		t.Errorf("delays = %v, want %v", clk.recorded(), want)
	}
}

func TestSendStopsWhenRetryDeclines(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	policy := alwaysRetry(3, time.Second)
	policy.Retry = func(resp *Response, err error) bool {
		return resp != nil && resp.StatusCode == http.StatusTooManyRequests
	}

	if _, _, err := newClient(newClock()).Send(t.Context(), request(server.URL), policy); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("made %d attempts, want 1", got)
	}
}

func TestSendBudgetStopsBeforeDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		budget   time.Duration
		duration time.Duration // how long each attempt takes
		delay    time.Duration
		want     int
	}{
		{"no budget", 0, time.Second, 500 * time.Millisecond, 3},
		{"ample budget", 30 * time.Second, 10 * time.Second, 5 * time.Second, 2},
		{"tight budget", 2500 * time.Millisecond, 750 * time.Millisecond, 500 * time.Millisecond, 2},
		{"zero delay still counts attempts", 2 * time.Second, time.Second, 0, 2},
		// elapsed 0 + delay 1s reaches the 1s budget exactly, so no retry is made.
		{"delay reaches budget", time.Second, 0, time.Second, 1},
		{"delay far over budget", time.Second, 0, time.Minute, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clk := newClock()
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				clk.advance(tt.duration)
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()

			policy := alwaysRetry(3, tt.delay)
			policy.Budget = tt.budget

			if _, _, err := newClient(clk).Send(t.Context(), request(server.URL), policy); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if got := int(attempts.Load()); got != tt.want {
				t.Errorf("made %d attempts, want %d", got, tt.want)
			}
		})
	}
}

func TestSendBudgetIsPerCall(t *testing.T) {
	t.Parallel()

	clk := newClock()
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		clk.advance(10 * time.Second)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := newClient(clk)
	policy := alwaysRetry(3, 5*time.Second)
	policy.Budget = 30 * time.Second

	for call := 1; call <= 2; call++ {
		attempts.Store(0)
		if _, _, err := client.Send(t.Context(), request(server.URL), policy); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if got := attempts.Load(); got != 2 {
			t.Errorf("call %d made %d attempts, want 2", call, got)
		}
	}
}

func TestSendTransportErrorRetried(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close() // nothing is listening, so every attempt fails to connect

	clk := newClock()
	resp, _, err := newClient(clk).Send(t.Context(), request(url), alwaysRetry(3, time.Millisecond))
	if err == nil {
		t.Fatalf("Send() = %+v, want a transport error", resp)
	}
	if got := len(clk.recorded()); got != 2 {
		t.Errorf("slept %d times, want 2", got)
	}
}

func TestSendPerAttemptTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	req := request(server.URL)
	req.Timeout = 50 * time.Millisecond

	policy := alwaysRetry(1, 0)
	_, _, err := newClient(newClock()).Send(t.Context(), req, policy)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send() error = %v, want a deadline", err)
	}
}

func TestSendCallerCancellationIsTerminal(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	var once sync.Once
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		// Drain the body: the server only starts detecting a closed connection
		// once the request body has hit EOF, and this handler waits for exactly
		// that signal.
		io.Copy(io.Discard, r.Body)
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-started
		cancel()
	}()

	_, _, err := newClient(newClock()).Send(ctx, request(server.URL), alwaysRetry(5, time.Millisecond))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() error = %v, want context.Canceled", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("made %d attempts, want 1: cancellation must not be retried", got)
	}
}

func TestSendCancellationDuringBackoff(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	client := newClient(newClock())
	client.Sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}

	if _, _, err := client.Send(ctx, request(server.URL), alwaysRetry(3, time.Second)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() error = %v, want context.Canceled", err)
	}
}

func TestSendGetWithoutBody(t *testing.T) {
	t.Parallel()

	var length int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		length = r.ContentLength
		w.Header().Set("X-Request-Id", "req-1")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	req := request(server.URL)
	req.Method = http.MethodGet
	req.Body = nil

	resp, _, err := newClient(newClock()).Send(t.Context(), req, alwaysRetry(1, 0))
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if length > 0 {
		t.Errorf("ContentLength = %d, want no body", length)
	}
	if string(resp.Body) != `{}` || resp.Header.Get("X-Request-Id") != "req-1" {
		t.Errorf("Response = %+v, want the body and headers carried through", resp)
	}
}

func TestSendDoesNotMutateRequestHeader(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	req := request(server.URL)
	if _, _, err := newClient(newClock()).Send(t.Context(), req, alwaysRetry(3, time.Millisecond)); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if req.Header.Get("X-Retry-Count") != "" {
		t.Errorf("Send mutated the caller's header: %v", req.Header)
	}
}

func TestSendConcurrent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Retry-Count") == "" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"call":"` + r.Header.Get("X-Call") + `"}`))
	}))
	defer server.Close()

	client := newClient(newClock())

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			req := request(server.URL)
			req.Header = http.Header{"X-Call": []string{strconv.Itoa(i)}}

			resp, _, err := client.Send(t.Context(), req, alwaysRetry(2, time.Millisecond))
			if err != nil {
				t.Errorf("Send() error = %v", err)
				return
			}
			if want := `{"call":"` + strconv.Itoa(i) + `"}`; string(resp.Body) != want {
				t.Errorf("Send() = %s, want %s", resp.Body, want)
			}
		}()
	}
	wg.Wait()
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func equalDurations(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSleep(t *testing.T) {
	t.Parallel()

	t.Run("waits", func(t *testing.T) {
		t.Parallel()

		start := time.Now()
		if err := Sleep(t.Context(), 20*time.Millisecond); err != nil {
			t.Fatalf("Sleep() error = %v", err)
		}
		if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
			t.Errorf("Sleep() returned after %v, want at least 20ms", elapsed)
		}
	})

	t.Run("zero returns immediately", func(t *testing.T) {
		t.Parallel()

		if err := Sleep(t.Context(), 0); err != nil {
			t.Fatalf("Sleep() error = %v", err)
		}
	})

	t.Run("cancellation cuts it short", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		if err := Sleep(ctx, time.Minute); !errors.Is(err, context.Canceled) {
			t.Fatalf("Sleep() error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("Sleep() waited %v, want it to stop on cancellation", elapsed)
		}
	})

	t.Run("already cancelled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if err := Sleep(ctx, 0); !errors.Is(err, context.Canceled) {
			t.Errorf("Sleep() error = %v, want context.Canceled", err)
		}
	})
}
