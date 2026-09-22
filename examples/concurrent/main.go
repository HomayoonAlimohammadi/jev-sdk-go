// Command concurrent classifies several tickets at once, with the parallelism
// bounded by the caller rather than the SDK.
//
// Set TYPESAFE_API_KEY, then: go run ./examples/concurrent
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

const concurrency = 4

var tickets = []string{
	"I was charged twice this month.",
	"How do I reset my password?",
	"Your service has been down for three hours. This is unacceptable.",
	"Thanks for the quick fix yesterday!",
}

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

	type result struct {
		ticket string
		answer *jev.SystemOneResponse
		err    error
	}
	results := make([]result, len(tickets))

	// One client, shared: it pools connections across every goroutine.
	slots := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, ticket := range tickets {
		wg.Add(1)
		go func() {
			defer wg.Done()

			slots <- struct{}{}
			defer func() { <-slots }()

			answer, err := client.SystemOne(ctx, jev.SystemOneRequest{
				State: ticket,
				Questions: map[string]jev.Question{
					"billing": jev.Noul{Instructions: "Is this about billing?"},
					"tone": jev.Choice{
						Criteria: map[string]any{"positive": nil, "neutral": nil, "negative": nil},
					},
				},
			})
			results[i] = result{ticket: ticket, answer: answer, err: err}
		}()
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			fmt.Printf("%-40.40s  error: %v\n", r.ticket, r.err)
			continue
		}
		fmt.Printf("%-40.40s  billing=%.2f tone=%s\n", r.ticket, r.answer.Nouls["billing"].Noul, r.answer.Choices["tone"].Choice)
	}
	return nil
}
