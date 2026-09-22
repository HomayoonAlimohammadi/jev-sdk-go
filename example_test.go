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

// Labels builds criteria for choices that speak for themselves, so the common
// case is not padded out with nil values.
func ExampleLabels() {
	question := jev.Choice{
		Instructions: "What is this ticket about?",
		Criteria:     jev.Labels("billing", "technical", "other"),
	}

	encoded, err := json.Marshal(question)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(encoded))

	// Output:
	// {"type":"choice","instructions":"What is this ticket about?","criteria":{"billing":null,"other":null,"technical":null}}
}

// A description sharpens the boundary between labels that overlap. Write the
// map out to mix described and undescribed choices.
func ExampleLabels_described() {
	question := jev.Choice{
		Instructions: "What is this ticket about?",
		Criteria: map[string]any{
			"billing":   "Payments, invoices and refunds, but not delivery complaints",
			"technical": map[string]any{"meaning": "The product misbehaved", "examples": []string{"500 error"}},
			"other":     nil,
		},
	}

	encoded, err := json.Marshal(question)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(encoded))

	// Output:
	// {"type":"choice","instructions":"What is this ticket about?","criteria":{"billing":"Payments, invoices and refunds, but not delivery complaints","other":null,"technical":{"examples":["500 error"],"meaning":"The product misbehaved"}}}
}

// Levels builds a score rubric from one phrase per level. Position is the
// score, counting from zero.
func ExampleLevels() {
	question := jev.Score{
		Instructions: "How urgent is this ticket?",
		Criteria:     jev.Levels("can wait", "this week", "today"),
	}

	encoded, err := json.Marshal(question)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(encoded))

	// Output:
	// {"type":"score","instructions":"How urgent is this ticket?","criteria":["can wait","this week","today"]}
}

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
				Criteria:     jev.Labels("calm", "frustrated", "angry"),
			},
			"urgency": jev.Score{
				Instructions: "How urgent is this ticket?",
				Criteria:     jev.Levels("can wait", "this week", "today"),
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
			"tone":    jev.Choice{Criteria: jev.Labels("calm", "angry")},
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
		Questions: map[string]jev.Question{"billing": jev.Noul{Instructions: "Is this about billing?"}},
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

// Team is a caller's own label type for a routing question.
type Team string

const (
	Billing   Team = "billing"
	Technical Team = "technical"
)

// Severity is a caller's own level type: iota numbering lines up with the
// rubric, where a description's position is its level.
type Severity int

const (
	Cosmetic Severity = iota
	Degraded
	Blocking
)

// Ask returns a typed handle per question, so the answers come back as your
// own types and the compiler checks what you do with them.
func ExampleAsk() {
	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	req := jev.SystemOneRequest{State: "Our integration returns 500 on every request."}
	urgent := jev.Ask(&req, "urgent", jev.Noul{Instructions: "Does this convey urgency?"})
	team := jev.Ask(&req, "team", jev.ChoiceOf[Team]{
		Instructions: "Which team should handle this?",
		Criteria:     map[Team]any{Billing: "Payments, invoices, refunds", Technical: "Bugs, outages, integrations"},
	})
	severity := jev.Ask(&req, "severity", jev.ScoreOf[Severity]{
		Instructions: "How severe is the issue?",
		Criteria:     jev.Levels("cosmetic", "degraded, with a workaround", "blocking"),
	})

	resp, err := client.SystemOne(context.Background(), req)
	if err != nil {
		panic(err)
	}

	u, _ := urgent.Answer(resp)
	t, _ := team.Answer(resp)
	s, _ := severity.Answer(resp)

	switch {
	case t.Confidence < 0.5:
		fmt.Println("route to a human; runner-up was", t.Ranked()[1])
	case t.Choice == Technical && s.Level() == Blocking && u.Noul > 0.8:
		fmt.Println("page on-call")
	default:
		fmt.Println("queue for", t.Choice)
	}
}

// Ranked orders the labels by probability, so the runner-up is one index away.
func ExampleChoiceAnswerOf_Ranked() {
	answer := jev.ChoiceAnswerOf[Team]{
		Choice:        Technical,
		Confidence:    0.62,
		Probabilities: map[Team]float64{Billing: 0.38, Technical: 0.62},
	}

	fmt.Println(answer.Ranked())
	// Output: [technical billing]
}

// A score is an expected value, so it can fall between levels. Level rounds it
// to the one it is nearest, in your own level type.
func ExampleScoreAnswerOf_Level() {
	answer := jev.ScoreAnswerOf[Severity]{
		Score:  1.8,
		Legend: map[Severity]any{Cosmetic: "cosmetic", Degraded: "degraded", Blocking: "blocking"},
	}

	fmt.Println(answer.Level() == Blocking, answer.Description())
	// Output: true blocking
}

// An answer of a kind this SDK version does not model is kept, not dropped, so
// a RawQuestion works end to end.
func ExampleUnknownAnswer() {
	client, err := jev.New()
	if err != nil {
		panic(err)
	}

	resp, err := client.SystemOne(context.Background(), jev.SystemOneRequest{
		State:     "a support ticket",
		Questions: map[string]jev.Question{"future": jev.RawQuestion{"type": "aurora", "instructions": "?"}},
	})
	if err != nil {
		panic(err)
	}

	if unknown, ok := resp.Answers["future"].(jev.UnknownAnswer); ok {
		var decoded map[string]any
		if err := json.Unmarshal(unknown.Raw, &decoded); err != nil {
			panic(err)
		}
		fmt.Println(unknown.Type, decoded)
	}
}
