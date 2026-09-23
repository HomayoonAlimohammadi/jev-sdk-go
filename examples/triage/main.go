// Command triage routes support tickets to a team, using the typed questions:
// the answers come back as this program's own Team and Severity types, so the
// routing switch is checked by the compiler.
//
// It shows Ask and typed keys, labels with descriptions, a score rubric read
// back as an enum, and gating on confidence with the runner-up for a human.
//
//	TYPESAFE_API_KEY=... go run ./examples/triage
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

// Team is where a ticket can go.
type Team string

const (
	Billing   Team = "billing"
	Technical Team = "technical"
	Account   Team = "account"
	Other     Team = "other"
)

// Severity is the impact on the customer. Its iota numbering matches the
// rubric below, where a description's position is its level.
type Severity int

const (
	Cosmetic Severity = iota
	Degraded
	Blocking
)

func (s Severity) String() string {
	return [...]string{"cosmetic", "degraded", "blocking"}[s]
}

// Below this confidence the team is a guess, and a person decides.
const minConfidence = 0.6

var tickets = []string{
	"Our checkout integration has returned HTTP 500 on every request since 09:00. Nothing is getting through.",
	"The invoice for August lists the wrong company address.",
	"How do I add a second admin to our workspace?",
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

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	for _, ticket := range tickets {
		decision, err := triage(ctx, client, ticket)
		if err != nil {
			return fmt.Errorf("triaging %q: %w", ticket, err)
		}
		fmt.Fprintf(out, "%-50.50s -> %s\n", ticket, decision)
	}
	return nil
}

func triage(ctx context.Context, client *jev.Client, ticket string) (string, error) {
	req := jev.SystemOneRequest{State: ticket}

	urgent := jev.Ask(&req, "urgent", jev.Noul{Instructions: "Does the customer need this resolved right now?"})

	// Descriptions draw the line between labels that could both fit; "other"
	// keeps the set closed.
	team := jev.Ask(&req, "team", jev.ChoiceOf[Team]{
		Instructions: "Which team should handle this ticket?",
		Criteria: map[Team]any{
			Billing:   "Payments, invoices, refunds and pricing",
			Technical: "Errors, outages, integrations and bugs",
			Account:   "Users, permissions, login and workspace settings",
			Other:     "Anything that fits none of the above",
		},
	})

	severity := jev.Ask(&req, "severity", jev.ScoreOf[Severity]{
		Instructions: "How badly is the customer affected?",
		Criteria: jev.Levels(
			"cosmetic: nothing is blocked",
			"degraded: something is broken but there is a workaround",
			"blocking: the customer cannot work",
		),
	})

	resp, err := client.SystemOne(ctx, req)
	if err != nil {
		return "", err
	}

	// Each read is typed by its question: no assertions, no string compares.
	u, err := urgent.Answer(resp)
	if err != nil {
		return "", err
	}
	t, err := team.Answer(resp)
	if err != nil {
		return "", err
	}
	s, err := severity.Answer(resp)
	if err != nil {
		return "", err
	}

	// The thresholds are policy, so they live here and not in the questions.
	switch {
	case t.Confidence < minConfidence:
		return fmt.Sprintf("human review: %s or %s? (confidence %.2f)", t.Choice, t.Ranked()[1], t.Confidence), nil
	case s.Level() == Blocking && u.Noul >= 0.7:
		return fmt.Sprintf("page %s on-call (%s, urgency %.2f)", t.Choice, s.Level(), u.Noul), nil
	default:
		return fmt.Sprintf("queue for %s (%s)", t.Choice, s.Level()), nil
	}
}
