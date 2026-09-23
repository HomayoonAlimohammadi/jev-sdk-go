// Command batch classifies many messages concurrently through one shared
// client, with the parallelism bounded by the caller rather than the SDK.
//
// It shows sharing one client across goroutines, bounding concurrency,
// keeping results in input order, a deadline over the whole run, and
// failures reported per item, with the request ID to quote to support,
// instead of losing the batch.
//
//	TYPESAFE_API_KEY=... go run ./examples/batch
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

// concurrency bounds the calls in flight at once.
const concurrency = 4

var messages = []string{
	"I was charged twice this month.",
	"How do I reset my password?",
	"Your service has been down for three hours. This is unacceptable.",
	"Thanks for the quick fix yesterday!",
	"Can I get a copy of last year's invoices?",
	"The mobile app crashes when I open settings.",
}

type result struct {
	message string
	billing float64
	tone    string
	err     error
}

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, out io.Writer, opts ...jev.ClientOption) error {
	// One client for the whole batch: it is safe for concurrent use and
	// shares its connections across every goroutine.
	client, err := jev.New(opts...)
	if err != nil {
		return err
	}

	// The deadline covers the whole batch, retries included.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	results := make([]result, len(messages)) // indexed, so input order survives
	slots := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	for i, message := range messages {
		wg.Add(1)
		go func() {
			defer wg.Done()

			slots <- struct{}{}
			defer func() { <-slots }()

			results[i] = classify(ctx, client, message)
		}()
	}
	wg.Wait()

	var failed int
	for _, r := range results {
		if r.err != nil {
			failed++
			fmt.Fprintf(out, "%-45.45s  error: %s\n", r.message, describe(r.err))
			continue
		}
		fmt.Fprintf(out, "%-45.45s  billing %.2f  tone %s\n", r.message, r.billing, r.tone)
	}
	fmt.Fprintf(out, "%d classified, %d failed\n", len(results)-failed, failed)
	return nil
}

func classify(ctx context.Context, client *jev.Client, message string) result {
	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{
		State: message,
		Questions: map[string]jev.Question{
			"billing": jev.Noul{Instructions: "Is this about billing?"},
			"tone":    jev.Choice{Instructions: "What is the tone?", Criteria: jev.Labels("positive", "neutral", "negative")},
		},
	})
	if err != nil {
		return result{message: message, err: err}
	}
	return result{message: message, billing: resp.Nouls["billing"].Noul, tone: resp.Choices["tone"].Choice}
}

// describe keeps a failure actionable: an API error carries the request ID
// support will ask for.
func describe(err error) string {
	var apiErr *jev.APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("%d %s (request %s)", apiErr.StatusCode, apiErr.Message, apiErr.RequestID)
	}
	return err.Error()
}
