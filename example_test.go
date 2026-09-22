package jev_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

func ExampleClient_SystemOne() {
	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	resp, err := client.SystemOne(context.Background(), jev.SystemOneRequest{
		State: "I was charged twice. Please fix this ASAP.",
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
		panic(err)
	}

	fmt.Println(resp.Nouls["billing"].Noul)
	fmt.Println(resp.Choices["tone"].Choice)
	fmt.Println(resp.Scores["urgency"].Score)
}

// Criteria descriptions are not limited to strings: any JSON-marshalable value
// works, so a criterion can carry examples or structure.
func ExampleClient_SystemOne_richCriteria() {
	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	_, err = client.SystemOne(context.Background(), jev.SystemOneRequest{
		State: map[string]any{"subject": "Charged twice", "body": "I see two charges of $49."},
		Questions: map[string]jev.Question{
			"duplicate": jev.Noul{
				Instructions: "Is this a duplicate charge?",
				Criteria: &jev.NoulCriteria{
					True:  map[string]any{"meaning": "Billed more than once", "examples": []string{"charged twice"}},
					False: "A single, expected charge",
				},
			},
		},
	})
	if err != nil {
		panic(err)
	}
}

// A question kind or field the API accepts before this package models it can
// be sent through as a RawQuestion.
func ExampleRawQuestion() {
	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	_, err = client.SystemOne(context.Background(), jev.SystemOneRequest{
		State: "a support ticket",
		Questions: map[string]jev.Question{
			"experimental": jev.RawQuestion{
				"type":         "noul",
				"instructions": "Is this urgent?",
				"weight":       3,
			},
		},
	})
	if err != nil {
		panic(err)
	}
}

// SystemOneAs decodes the response body into a type of your own. The body is
// decoded as the API sends it, so the struct mirrors the wire shape.
func ExampleClient_SystemOneAs() {
	type ticketAnswers struct {
		jev.ResponseMeta // optional: filled in with the request ID and headers

		Answers struct {
			Billing jev.NoulAnswer   `json:"billing"`
			Tone    jev.ChoiceAnswer `json:"tone"`
		} `json:"answers"`
	}

	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	answers, err := client.SystemOneAs[ticketAnswers](context.Background(), jev.SystemOneRequest{
		State: "I was charged twice.",
		Questions: map[string]jev.Question{
			"billing": jev.Noul{Instructions: "Is this about billing?"},
			"tone":    jev.Choice{Criteria: map[string]any{"calm": nil, "angry": nil}},
		},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(answers.Answers.Billing.Noul, answers.RequestID)
}

func ExampleClient_ListModels() {
	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	resp, err := client.ListModels(context.Background())
	if err != nil {
		panic(err)
	}

	for _, model := range resp.Models {
		fmt.Printf("%s (%s): %s\n", model.Name, model.ReleaseDate, model.Description)
	}
}

// Errors carry the detail needed to decide what to do next.
func ExampleAPIError() {
	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	_, err = client.ListModels(context.Background())

	var apiErr *jev.APIError
	var transportErr *jev.TransportError
	var responseErr *jev.ResponseError

	switch {
	case err == nil:
	case errors.Is(err, jev.ErrRateLimited):
		errors.As(err, &apiErr)
		fmt.Printf("rate limited, retry in %v (request %s)\n", apiErr.RetryAfter, apiErr.RequestID)
	case errors.Is(err, jev.ErrUnauthorized):
		fmt.Println("check TYPESAFE_API_KEY")
	case errors.As(err, &transportErr):
		fmt.Printf("never reached the API after %d attempts: %v\n", transportErr.Attempts, transportErr.Err)
	case errors.As(err, &responseErr):
		fmt.Printf("unusable response at %s\n", responseErr.Field)
	default:
		fmt.Println(err)
	}
}

// Retries are configured per client, and can be replaced for a single call.
func ExampleRetryPolicy() {
	policy := jev.DefaultRetryPolicy()
	policy.MaxRetries = 4
	policy.BackoffMax = 10 * time.Second
	policy.Budget = time.Minute

	client, err := jev.New(jev.WithRetry(policy))
	if err != nil {
		panic(err)
	}

	// This one call must not be retried at all.
	_, err = client.ListModels(context.Background(), jev.WithCallRetry(jev.RetryPolicy{}))
	if err != nil {
		panic(err)
	}
}

// The client is safe to share across goroutines; the caller chooses the
// parallelism.
func ExampleClient_concurrent() {
	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	tickets := []string{"I was charged twice.", "How do I reset my password?"}
	results := make([]*jev.SystemOneResponse, len(tickets))

	var wg sync.WaitGroup
	for i, ticket := range tickets {
		wg.Add(1)
		go func() {
			defer wg.Done()

			resp, err := client.SystemOne(context.Background(), jev.SystemOneRequest{
				State:     ticket,
				Questions: map[string]jev.Question{"billing": jev.Noul{Instructions: "Is this about billing?"}},
			})
			if err != nil {
				return
			}
			results[i] = resp
		}()
	}
	wg.Wait()
}

// The SDK logs nothing until it is given a logger. Credential-bearing headers
// are redacted; bodies are logged verbatim at debug level.
func ExampleWithLogger() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	client, err := jev.New(jev.WithLogger(logger))
	if err != nil {
		panic(err)
	}
	_ = client
}

// The raw response body is always available, for answer kinds or fields this
// version does not model.
func ExampleSystemOneResponse_Raw() {
	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	resp, err := client.SystemOne(context.Background(), jev.SystemOneRequest{
		State:     "a support ticket",
		Questions: map[string]jev.Question{"billing": jev.Noul{}},
	})
	if err != nil {
		panic(err)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Raw, &body); err != nil {
		panic(err)
	}
	fmt.Println(body["answers"])
}
