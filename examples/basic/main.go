// Command basic is the quickstart: it lists the models available to the
// account, then asks one of each kind of question about a support ticket and
// reads the answers back.
//
//	TYPESAFE_API_KEY=... go run ./examples/basic
package main

import (
	"context"
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

// run takes extra client options last, so they override the defaults; the
// example's test uses them to point it at a fake API.
func run(ctx context.Context, out io.Writer, opts ...jev.ClientOption) error {
	client, err := jev.New(opts...) // reads TYPESAFE_API_KEY
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		return err
	}
	for _, model := range models.Models {
		fmt.Fprintf(out, "model %s, released %s\n", model.Name, model.ReleaseDate)
	}

	// Independent questions about the same state belong in one call.
	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{
		State: "I was charged twice this month. Please refund one of the charges.",
		Questions: map[string]jev.Question{
			"billing": jev.Noul{Instructions: "Is this ticket about billing?"},
			"tone": jev.Choice{
				Instructions: "What is the customer's tone?",
				Criteria:     jev.Labels("calm", "frustrated", "angry"),
			},
			"urgency": jev.Score{
				Instructions: "How urgent is this ticket?",
				Criteria:     jev.Levels("can wait", "this week", "today"),
			},
		},
	})
	if err != nil {
		return err
	}

	// Every question asked is guaranteed an answer of its kind, so these
	// lookups never read a zero value in place of a missing answer.
	tone := resp.Choices["tone"]
	urgency := resp.Scores["urgency"]

	fmt.Fprintf(out, "answered by %s (request %s)\n", resp.Model, resp.RequestID)
	fmt.Fprintf(out, "billing: %.2f probability of yes\n", resp.Nouls["billing"].Noul)
	fmt.Fprintf(out, "tone:    %s, confidence %.2f, runner-up %s\n", tone.Choice, tone.Confidence, tone.Ranked()[1])
	fmt.Fprintf(out, "urgency: %.2f on a 0-2 scale, nearest level %q\n", urgency.Score, urgency.Description())
	return nil
}
