// Command decode reads the response straight into a struct of this program's
// own, with SystemOneAs. The struct mirrors the wire shape, and its answer
// fields use the SDK's generic answer types, so labels and levels arrive as
// this program's own types.
//
// Use it when the response feeds another system as-is, or when a partial
// response is acceptable: unlike SystemOne, SystemOneAs checks nothing, so
// the answers here are pointers and absence stays visible.
//
//	TYPESAFE_API_KEY=... go run ./examples/decode
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

type Product string

const (
	Payments Product = "payments"
	Payroll  Product = "payroll"
	Cards    Product = "cards"
)

type Satisfaction int

const (
	VeryUnhappy Satisfaction = iota
	Neutral
	Happy
)

// report mirrors the response body. Embedding ResponseMeta opts it in to the
// request ID, status and headers.
type report struct {
	jev.ResponseMeta

	Model   string `json:"model"`
	Answers struct {
		Refund       *jev.NoulAnswer                  `json:"refund"`
		Product      *jev.ChoiceAnswerOf[Product]     `json:"product"`
		Satisfaction *jev.ScoreAnswerOf[Satisfaction] `json:"satisfaction"`
	} `json:"answers"`
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

	req := jev.SystemOneRequest{
		State: "My payroll run failed and my staff were not paid. This is a disaster. Refund this month's fee.",
		Questions: map[string]jev.Question{
			"refund":       jev.Noul{Instructions: "Does the customer ask for a refund?"},
			"product":      jev.ChoiceOf[Product]{Instructions: "Which product is this about?", Criteria: jev.LabelsOf(Payments, Payroll, Cards)},
			"satisfaction": jev.ScoreOf[Satisfaction]{Instructions: "How satisfied is the customer?", Criteria: jev.Levels("very unhappy", "neutral", "happy")},
		},
	}

	r, err := jev.SystemOneAs[report](ctx, client, req)
	if err != nil {
		return err
	}

	// Nothing was checked for us, so check what this program depends on.
	if r.Answers.Refund == nil || r.Answers.Product == nil || r.Answers.Satisfaction == nil {
		return errors.New("the response left a question unanswered")
	}

	fmt.Fprintf(out, "model %s, request %s, status %d\n", r.Model, r.RequestID, r.StatusCode)
	fmt.Fprintf(out, "refund:       %.2f\n", r.Answers.Refund.Noul)
	fmt.Fprintf(out, "product:      %s\n", r.Answers.Product.Choice)

	// Level returns a Satisfaction, so this switch is over the program's own
	// constants.
	switch r.Answers.Satisfaction.Level() {
	case VeryUnhappy:
		fmt.Fprintln(out, "satisfaction: very unhappy, escalate")
	case Neutral:
		fmt.Fprintln(out, "satisfaction: neutral")
	case Happy:
		fmt.Fprintln(out, "satisfaction: happy")
	}
	return nil
}
