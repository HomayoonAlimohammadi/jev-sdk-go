// Command moderation screens a forum post against several policies at once
// and turns the probabilities into allow, review or remove.
//
// It shows a structured state (a Go struct), yes/no questions whose outcomes
// are described with structured criteria, and a policy table that keeps the
// thresholds in code, where they can change without asking anything again.
//
//	TYPESAFE_API_KEY=... go run ./examples/moderation
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"slices"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

// post is the state: any JSON-encodable value works, and context the model
// should weigh (who wrote it, where) travels with the text.
type post struct {
	Channel string `json:"channel"`
	Author  author `json:"author"`
	Text    string `json:"text"`
}

type author struct {
	AccountAgeDays int `json:"account_age_days"`
	PriorFlags     int `json:"prior_flags"`
}

// policy is one rule: the question that detects it, and the probabilities at
// which a post is sent for review or removed outright. The costs of a missed
// violation differ by rule, so the thresholds do too.
type policy struct {
	question jev.Noul
	review   float64
	remove   float64
}

var policies = map[string]policy{
	"spam": {
		question: jev.Noul{
			Instructions: "Is this post spam?",
			Criteria: &jev.NoulCriteria{
				True: map[string]any{
					"meaning":  "Unsolicited promotion of a product, service or link",
					"examples": []string{"buy followers now", "limited offer, click here"},
				},
				False: "A genuine contribution to the discussion, even if it mentions a product",
			},
		},
		review: 0.6, remove: 0.9,
	},
	"harassment": {
		question: jev.Noul{
			Instructions: "Does this post harass or demean a person?",
			Criteria:     &jev.NoulCriteria{True: "Insults, threats or slurs aimed at someone", False: "Disagreement, even heated, about ideas"},
		},
		review: 0.4, remove: 0.85,
	},
	"personal_data": {
		question: jev.Noul{
			Instructions: "Does this post expose someone's personal data?",
			Criteria:     &jev.NoulCriteria{True: "An email, phone number, home address or ID number of a person"},
		},
		// A leak cannot be undone, so even a modest probability gets a look.
		review: 0.3, remove: 0.8,
	},
}

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, out io.Writer, opts ...jev.ClientOption) error {
	client, err := jev.New(opts...)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	state := post{
		Channel: "public",
		Author:  author{AccountAgeDays: 2, PriorFlags: 1},
		Text:    "Great thread! DM me for cheap followers, or call me at 555-0142.",
	}

	// Every policy is checked in one call; the questions are independent.
	questions := make(map[string]jev.Question, len(policies))
	for name, p := range policies {
		questions[name] = p.question
	}

	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{State: state, Questions: questions})
	if err != nil {
		return err
	}

	verdict := "allow"
	for _, name := range slices.Sorted(maps.Keys(policies)) {
		p := policies[name]
		probability := resp.Nouls[name].Noul

		action := "ok"
		switch {
		case probability >= p.remove:
			action, verdict = "remove", "remove"
		case probability >= p.review:
			action = "review"
			if verdict == "allow" {
				verdict = "review"
			}
		}
		fmt.Fprintf(out, "%-14s %.2f  %s\n", name, probability, action)
	}
	fmt.Fprintf(out, "verdict: %s\n", verdict)
	return nil
}
