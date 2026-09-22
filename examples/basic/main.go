// Command basic asks a few questions about one support ticket.
//
// Set TYPESAFE_API_KEY, then: go run ./examples/basic
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	client, err := jev.New()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{
		State: map[string]any{
			"subject": "Charged twice this month",
			"body":    "I see two charges of $49. I only have one account. Please fix this ASAP.",
		},
		Questions: map[string]jev.Question{
			"billing": jev.Noul{Instructions: "Is this ticket about billing?"},
			"tone": jev.Choice{
				Instructions: "What is the customer's tone?",
				Criteria:     map[string]any{"calm": nil, "frustrated": nil, "angry": nil},
			},
			"urgency": jev.Score{
				Instructions: "How urgent is this ticket?",
				Criteria:     []any{"can wait", "this week", "today"},
			},
		},
	})
	if err != nil {
		var apiErr *jev.APIError
		if errors.As(err, &apiErr) {
			fmt.Fprintf(os.Stderr, "request %s failed\n", apiErr.RequestID)
		}
		return err
	}

	fmt.Printf("model:   %s\n", resp.Model)
	fmt.Printf("billing: %.2f\n", resp.Nouls["billing"].Noul)
	fmt.Printf("tone:    %s (%.2f)\n", resp.Choices["tone"].Choice, resp.Choices["tone"].Confidence)
	fmt.Printf("urgency: %.2f of %d\n", resp.Scores["urgency"].Score, len(resp.Scores["urgency"].Legend)-1)
	return nil
}
