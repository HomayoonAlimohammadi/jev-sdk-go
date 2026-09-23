// Command openrouter calls Jev through OpenRouter, which serves the System
// One API at its own base URL, authenticated with an OpenRouter key. The SDK
// needs no changes, only configuration:
//
//	OPENROUTER_API_KEY=... go run ./examples/openrouter
//
// Setting TYPESAFE_BASE_URL=https://openrouter.ai/api and TYPESAFE_API_KEY to
// the OpenRouter key does the same without code. OpenRouter's guide is at
// https://openrouter.ai/docs/guides/community/typesafe-sdk.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

// openRouterBaseURL is OpenRouter's API root; the SDK appends /v1/systemone.
const openRouterBaseURL = "https://openrouter.ai/api"

func main() {
	// Checked here so the error names the variable this program reads; the
	// SDK's own message would point at TYPESAFE_API_KEY.
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		log.Fatal("set OPENROUTER_API_KEY to an OpenRouter API key")
	}
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, out io.Writer, opts ...jev.ClientOption) error {
	client, err := jev.New(append([]jev.ClientOption{
		jev.WithBaseURL(openRouterBaseURL),
		jev.WithAPIKey(os.Getenv("OPENROUTER_API_KEY")),
		// No WithModel needed: OpenRouter maps the default jev-latest onto
		// its own ~typesafe/jev-latest.
	}, opts...)...)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{
		State: "Please cancel my subscription and delete my account.",
		Questions: map[string]jev.Question{
			"churn":  jev.Noul{Instructions: "Is the customer leaving?"},
			"intent": jev.Choice{Instructions: "What does the customer want?", Criteria: jev.Labels("cancel", "downgrade", "complain", "other")},
		},
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "churn %.2f, intent %s\n", resp.Nouls["churn"].Noul, resp.Choices["intent"].Choice)

	// OpenRouter adds id, provider and usage.cost to the response. The SDK
	// tolerates them without modelling them; the raw body has them.
	var extra struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		Usage    struct {
			Cost *float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(resp.Raw, &extra); err != nil {
		return err
	}
	if extra.Usage.Cost != nil {
		fmt.Fprintf(out, "generation %s via %s, cost $%.6f\n", extra.ID, extra.Provider, *extra.Usage.Cost)
	}

	// OpenRouter's /v1/models is its own catalog, not TypeSafe's model list,
	// so ListModels cannot decode it. Browse models at openrouter.ai instead.
	_, err = client.ListModels(ctx)
	var responseErr *jev.ResponseError
	if errors.As(err, &responseErr) {
		fmt.Fprintln(out, "model listing is not available through OpenRouter")
	}
	return nil
}
