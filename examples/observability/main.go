// Command observability watches the SDK at work: structured logs through a
// slog logger, and per-attempt metrics from a wrapped HTTP transport.
//
// The transport is the seam for tracing too. Swap the meter below for
// otelhttp.NewTransport(http.DefaultTransport) and every attempt, retries
// included, becomes an OpenTelemetry span, with no tracing dependency in the
// SDK itself.
//
//	TYPESAFE_API_KEY=... go run ./examples/observability
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

// meter records every attempt that passes through it. A RoundTripper must not
// modify the request, and this one only reads it.
type meter struct {
	next http.RoundTripper

	mu       sync.Mutex
	attempts int
	retries  int
	elapsed  time.Duration
}

func (m *meter) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := m.next.RoundTrip(r)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts++
	m.elapsed += time.Since(start)
	// The SDK numbers each retry in this header, so a transport can tell
	// first attempts from retries.
	if r.Header.Get("X-TypeSafe-Retry-Count") != "" {
		m.retries++
	}
	return resp, err
}

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, out io.Writer, opts ...jev.ClientOption) error {
	m := &meter{next: http.DefaultTransport}
	httpClient := &http.Client{
		Transport: m,
		// A client of your own follows redirects unless told not to. Refuse
		// them, as the SDK's own client does, so the key never follows one.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// Info logs one line per response and per retry. Debug adds headers,
	// with credentials redacted; bodies stay out unless WithBodyLogging.
	logger := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))

	client, err := jev.New(append([]jev.ClientOption{
		jev.WithHTTPClient(httpClient),
		jev.WithLogger(logger),
	}, opts...)...)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{
		State:     "The dashboard has been loading for five minutes.",
		Questions: map[string]jev.Question{"outage": jev.Noul{Instructions: "Does this report an outage?"}},
	})
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// The request ID is what to quote to support; usage is what was billed.
	fmt.Fprintf(out, "request %s: outage %.2f\n", resp.RequestID, resp.Nouls["outage"].Noul)
	if resp.Usage.InputTokens != nil {
		fmt.Fprintf(out, "input tokens: %d\n", *resp.Usage.InputTokens)
	}
	fmt.Fprintf(out, "attempts: %d, of which retries: %d, time on the wire: %v\n", m.attempts, m.retries, m.elapsed.Round(time.Millisecond))
	return nil
}
