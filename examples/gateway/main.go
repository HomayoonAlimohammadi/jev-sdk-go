// Command gateway reaches the API through a self-hosted gateway or proxy, the
// shape many platform teams put in front of third-party APIs to centralize
// credentials, quotas and audit.
//
// It shows a base URL with a path prefix, a gateway credential alongside the
// API key, headers on every request and on a single call, and an HTTP client
// tuned for a service making many calls to one host.
//
//	GATEWAY_URL=https://gateway.example.com/typesafe GATEWAY_KEY=... TYPESAFE_API_KEY=... go run ./examples/gateway
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
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
	// Start from the default transport and tune it. MaxIdleConnsPerHost
	// matters here: net/http keeps only two idle connections per host, and
	// every call goes to the same host. The SDK's own client is sized this way
	// already; a client of your own has to be told.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.MaxIdleConnsPerHost = 32
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}

	httpClient := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	client, err := jev.New(append([]jev.ClientOption{
		// The SDK appends /v1/systemone to the prefix: .../typesafe/v1/systemone.
		jev.WithBaseURL(envOr("GATEWAY_URL", "https://gateway.example.com/typesafe")),
		jev.WithHTTPClient(httpClient),
		// The gateway's own credential rides alongside the API key, which the
		// SDK sends as the bearer token. Header names containing "key" are
		// redacted from logs.
		jev.WithHeader("X-Gateway-Key", os.Getenv("GATEWAY_KEY")),
		jev.WithHeader("X-Team", "support-tooling"),
	}, opts...)...)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	ticketID := "T-48213"
	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{
		State:     "The export to CSV times out for reports over a year long.",
		Questions: map[string]jev.Question{"bug": jev.Noul{Instructions: "Is this a bug report?"}},
	},
		// A per-call header, for the gateway's audit log.
		jev.WithCallHeader("X-Ticket-Id", ticketID),
	)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "ticket %s: bug %.2f (request %s)\n", ticketID, resp.Nouls["bug"].Noul, resp.RequestID)
	return nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
