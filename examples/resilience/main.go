// Command resilience configures how calls are retried and bounded in time,
// and turns every kind of failure into a decision.
//
// It shows a retry policy built from the default, a per-attempt timeout, a
// per-call override for a latency-sensitive path, requests refused before
// they are sent, and one switch that tells rate limits, bad credentials,
// outages and malformed responses apart.
//
//	TYPESAFE_API_KEY=... go run ./examples/resilience
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, out io.Writer, opts ...jev.ClientOption) error {
	// Start from the default and adjust: the zero RetryPolicy retries nothing.
	policy := jev.DefaultRetryPolicy()
	policy.MaxRetries = 4                   // five attempts in all
	policy.BackoffMax = 10 * time.Second    // cap on the computed backoff
	policy.Budget = 45 * time.Second        // cap on the whole call, delays included
	policy.MaxRetryAfter = 20 * time.Second // a server asking for longer is not waited out

	client, err := jev.New(append([]jev.ClientOption{
		jev.WithRetry(policy),
		jev.WithTimeout(15 * time.Second), // each attempt; the context bounds the call
	}, opts...)...)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	question := map[string]jev.Question{"urgent": jev.Noul{Instructions: "Is this urgent?"}}

	// A request the API would refuse fails locally: no call, no retry.
	_, err = client.SystemOne(ctx, jev.SystemOneRequest{
		State:     "Checkout is down.",
		Questions: map[string]jev.Question{"severity": jev.Score{Instructions: "How severe?"}},
	})
	fmt.Fprintf(out, "rubric without levels: %s\n", describe(err))

	// The normal path: transient failures are retried under the policy.
	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{State: "Checkout is down.", Questions: question})
	if err != nil {
		fmt.Fprintf(out, "retried call:          %s\n", describe(err))
	} else {
		fmt.Fprintf(out, "retried call:          urgent %.2f (request %s)\n", resp.Nouls["urgent"].Noul, resp.RequestID)
	}

	// A latency-sensitive path fails fast rather than waiting out retries.
	resp, err = client.SystemOne(ctx, jev.SystemOneRequest{State: "Checkout is down.", Questions: question},
		jev.WithCallRetry(jev.RetryPolicy{}),
		jev.WithCallTimeout(3*time.Second),
	)
	if err != nil {
		fmt.Fprintf(out, "fail-fast call:        %s\n", describe(err))
	} else {
		fmt.Fprintf(out, "fail-fast call:        urgent %.2f\n", resp.Nouls["urgent"].Noul)
	}
	return nil
}

// describe maps a failure to what the program should do about it. It branches
// with errors.Is and errors.As, never on the message.
func describe(err error) string {
	var (
		apiErr       *jev.APIError
		transportErr *jev.TransportError
		responseErr  *jev.ResponseError
	)

	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, jev.ErrInvalidQuestion), errors.Is(err, jev.ErrInvalidState), errors.Is(err, jev.ErrNoQuestions):
		return "refused before sending, a bug to fix: " + err.Error()
	case errors.Is(err, jev.ErrRateLimited):
		errors.As(err, &apiErr)
		return fmt.Sprintf("rate limited after %d attempts; retry in %v", apiErr.Attempts, apiErr.RetryAfter)
	case errors.Is(err, jev.ErrUnauthorized), errors.Is(err, jev.ErrForbidden):
		return "credentials rejected; fix configuration, do not retry"
	case errors.Is(err, jev.ErrServer):
		errors.As(err, &apiErr)
		return fmt.Sprintf("API unavailable after %d attempts (request %s)", apiErr.Attempts, apiErr.RequestID)
	case errors.As(err, &apiErr):
		return fmt.Sprintf("API refused the request: %d %s", apiErr.StatusCode, apiErr.Message)
	case errors.As(err, &transportErr) && transportErr.Timeout():
		return fmt.Sprintf("timed out after %d attempts", transportErr.Attempts)
	case errors.As(err, &transportErr):
		return fmt.Sprintf("never reached the API after %d attempts: %v", transportErr.Attempts, transportErr.Err)
	case errors.As(err, &responseErr):
		return "unusable response at " + responseErr.Field
	default:
		return err.Error()
	}
}
