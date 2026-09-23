// Command config asks questions defined in data rather than code: a JSON
// document names each question and its criteria, and the program forwards
// them untouched. Product teams can change what is asked without a release.
//
// It shows RawQuestion, reading answers of whatever kind the configuration
// asked for with one type switch, UnknownAnswer for a kind this SDK version
// does not model, and Raw for fields it does not decode.
//
//	TYPESAFE_API_KEY=... go run ./examples/config
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"slices"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

// questionsConfig would normally be read from a file or a config service.
const questionsConfig = `{
  "refund_requested": {"type": "noul", "instructions": "Does the customer ask for a refund?"},
  "product": {
    "type": "choice",
    "instructions": "Which product is this about?",
    "criteria": {"payments": null, "payroll": null, "cards": "Physical or virtual payment cards"}
  },
  "satisfaction": {
    "type": "score",
    "instructions": "How satisfied is the customer?",
    "criteria": ["very unhappy", "neutral", "happy"]
  }
}`

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, out io.Writer, opts ...jev.ClientOption) error {
	var configured map[string]jev.RawQuestion
	if err := json.Unmarshal([]byte(questionsConfig), &configured); err != nil {
		return fmt.Errorf("reading question config: %w", err)
	}

	questions := make(map[string]jev.Question, len(configured))
	for name, question := range configured {
		questions[name] = question
	}

	client, err := jev.New(opts...)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	// A malformed configuration fails here, before anything is sent: a raw
	// question still needs a type, and a choice or score its criteria.
	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{
		State:     "I was charged twice on my card and nobody is answering. I want my money back.",
		Questions: questions,
	})
	if err != nil {
		return err
	}

	// The program does not know in advance which kinds the config asks for,
	// so it reads every answer through one switch. The default branch keeps
	// it working when the API gains a kind this SDK version does not model.
	for _, name := range slices.Sorted(maps.Keys(resp.Answers)) {
		switch answer := resp.Answers[name].(type) {
		case jev.NoulAnswer:
			fmt.Fprintf(out, "%-17s yes with probability %.2f\n", name, answer.Noul)
		case jev.ChoiceAnswer:
			fmt.Fprintf(out, "%-17s %s (confidence %.2f)\n", name, answer.Choice, answer.Confidence)
		case jev.ScoreAnswer:
			fmt.Fprintf(out, "%-17s %.2f, nearest %q\n", name, answer.Score, answer.Description())
		case jev.UnknownAnswer:
			fmt.Fprintf(out, "%-17s %s answer, left raw: %s\n", name, answer.Type, answer.Raw)
		}
	}

	// Fields the SDK does not decode are still in the raw body.
	var tokens struct {
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(resp.Raw, &tokens); err == nil {
		fmt.Fprintf(out, "usage as sent: %v\n", tokens.Usage)
	}
	return nil
}
